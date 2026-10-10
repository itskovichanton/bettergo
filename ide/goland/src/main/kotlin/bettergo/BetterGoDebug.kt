package bettergo

import com.google.gson.Gson
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import com.intellij.execution.ExecutionException
import com.intellij.execution.configurations.RunProfile
import com.intellij.execution.configurations.RunnerSettings
import com.intellij.execution.executors.DefaultDebugExecutor
import com.intellij.execution.executors.DefaultRunExecutor
import com.intellij.execution.process.OSProcessHandler
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.runners.DefaultProgramRunner
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.execution.runners.ProgramRunner
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.vfs.LocalFileSystem
import com.intellij.xdebugger.XDebugProcess
import com.intellij.xdebugger.XDebugProcessStarter
import com.intellij.xdebugger.XDebugSession
import com.intellij.xdebugger.XDebuggerManager
import com.intellij.xdebugger.XDebuggerUtil
import com.intellij.xdebugger.XSourcePosition
import com.intellij.xdebugger.breakpoints.XBreakpointProperties
import com.intellij.xdebugger.breakpoints.XLineBreakpointType
import com.intellij.xdebugger.evaluation.XDebuggerEditorsProvider
import com.intellij.xdebugger.frame.XCompositeNode
import com.intellij.xdebugger.frame.XExecutionStack
import com.intellij.xdebugger.frame.XStackFrame
import com.intellij.xdebugger.frame.XSuspendContext
import com.intellij.xdebugger.frame.XValue
import com.intellij.xdebugger.frame.XValueChildrenList
import com.intellij.xdebugger.frame.XValueNode
import com.intellij.xdebugger.frame.XValuePlace
import java.io.BufferedReader
import java.io.File
import java.io.InputStreamReader
import java.io.OutputStreamWriter
import java.net.ServerSocket
import java.net.Socket

class BetterGoLineBreakpointType : XLineBreakpointType<XBreakpointProperties<*>>("bettergo-line", "BetterGo line breakpoint") {
    override fun canPutAt(file: com.intellij.openapi.vfs.VirtualFile, line: Int, project: com.intellij.openapi.project.Project) =
        file.fileType == BetterGoFileType.INSTANCE

    override fun createBreakpointProperties(file: com.intellij.openapi.vfs.VirtualFile, line: Int): XBreakpointProperties<*>? = null
}

class BetterGoRunner : DefaultProgramRunner() {
    override fun getRunnerId() = "BetterGoRun"
    override fun canRun(executorId: String, profile: RunProfile) =
        executorId == DefaultRunExecutor.EXECUTOR_ID && profile is BetterGoRunConfiguration
}

class BetterGoDebugRunner : ProgramRunner<RunnerSettings> {
    override fun getRunnerId() = "BetterGoDebug"
    override fun canRun(executorId: String, profile: RunProfile) =
        executorId == DefaultDebugExecutor.EXECUTOR_ID && profile is BetterGoRunConfiguration

    override fun execute(environment: ExecutionEnvironment) {
        val manager = XDebuggerManager.getInstance(environment.project)
        manager.startSessionAndShowTab(
            environment.runProfile.name,
            object : XDebugProcessStarter() {
                override fun start(session: XDebugSession): XDebugProcess = BetterGoDebugProcess(session, environment)
            },
            environment,
        )
    }
}

class BetterGoDebugProcess(session: XDebugSession, environment: ExecutionEnvironment) : XDebugProcess(session) {
    private val handler: ProcessHandler
    private val dlv: Process
    private val port: Int
    private var client: DelveClient? = null
    private var stopped = false

    init {
        val cfg = environment.runProfile as? BetterGoRunConfiguration
        val wd = cfg?.resolvedPackageDir()?.ifBlank { null } ?: environment.project.basePath ?: "."
        if (!File(wd).isDirectory) {
            throw ExecutionException("BetterGo package directory does not exist: $wd")
        }
        val pkg = if (cfg?.resolvedPackageDir().isNullOrBlank()) "./example" else "."
        val gen = ProcessBuilder(Tools.executable("bettergo"), "gen", wd).directory(File(wd)).redirectErrorStream(true)
        Tools.enrichPath(gen.environment())
        val genProc = gen.start()
        val genOut = genProc.inputStream.bufferedReader().readText()
        if (genProc.waitFor() != 0) {
            throw ExecutionException(genOut.lineSequence().firstOrNull { it.isNotBlank() } ?: "bettergo gen failed")
        }
        port = ServerSocket(0).use { it.localPort }
        val dlvBuilder = ProcessBuilder(
            Tools.executable("dlv"), "debug",
            "--headless",
            "--listen=127.0.0.1:$port",
            "--api-version=2",
            "--accept-multiclient",
            pkg
        ).directory(File(wd)).redirectErrorStream(true)
        Tools.enrichPath(dlvBuilder.environment())
        dlv = dlvBuilder.start()
        handler = OSProcessHandler(dlv, "dlv debug $pkg")
    }

    override fun doGetProcessHandler(): ProcessHandler = handler

    override fun getEditorsProvider(): XDebuggerEditorsProvider =
        object : XDebuggerEditorsProvider() {
            override fun getFileType() = BetterGoFileType.INSTANCE
            override fun createDocument(project: com.intellij.openapi.project.Project, text: String, sourcePosition: XSourcePosition?, mode: com.intellij.xdebugger.evaluation.EvaluationMode) =
                com.intellij.openapi.editor.EditorFactory.getInstance().createDocument(text)
        }

    override fun sessionInitialized() {
        ApplicationManager.getApplication().executeOnPooledThread {
            try {
                val c = DelveClient.connect(port, dlv)
                client = c
                val bps = XDebuggerManager.getInstance(session.project).breakpointManager.allBreakpoints
                for (bp in bps) {
                    if (bp.type !is BetterGoLineBreakpointType) continue
                    val pos = bp.sourcePosition ?: continue
                    val reply = c.call(
                        "RPCServer.CreateBreakpoint",
                        mapOf("Breakpoint" to mapOf("file" to pos.file.path, "line" to pos.line + 1))
                    )
                    if (reply.get("error") == null || reply.get("error").isJsonNull) {
                        val lineBp = bp as? com.intellij.xdebugger.breakpoints.XLineBreakpoint<*> ?: continue
                        ApplicationManager.getApplication().invokeLater { session.setBreakpointVerified(lineBp) }
                    }
                }
                command("continue")
            } catch (e: Exception) {
                if (!stopped) {
                    ApplicationManager.getApplication().invokeLater {
                        session.reportError(e.message ?: "delve failed")
                        session.stop()
                    }
                }
            }
        }
    }

    override fun resume(context: XSuspendContext?) {
        ApplicationManager.getApplication().executeOnPooledThread { command("continue") }
    }

    override fun startStepOver(context: XSuspendContext?) {
        ApplicationManager.getApplication().executeOnPooledThread { command("next") }
    }

    override fun startStepInto(context: XSuspendContext?) {
        ApplicationManager.getApplication().executeOnPooledThread { command("step") }
    }

    override fun startStepOut(context: XSuspendContext?) {
        ApplicationManager.getApplication().executeOnPooledThread { command("stepout") }
    }

    override fun stop() {
        stopped = true
        client?.close()
        handler.destroyProcess()
    }

    private fun command(name: String) {
        if (stopped) return
        val c = client ?: return
        val reply = c.call("RPCServer.Command", mapOf("name" to name))
        val state = reply.getAsJsonObject("result")?.getAsJsonObject("State")
            ?: reply.getAsJsonObject("result")?.getAsJsonObject("state")
        if (state?.get("exited")?.asBoolean == true) {
            ApplicationManager.getApplication().invokeLater { session.stop() }
            return
        }
        val thread = state?.getAsJsonObject("currentThread")
        val frames = stackFrames().ifEmpty {
            val file = thread?.get("file")?.asString
            val line = thread?.get("line")?.asInt
            if (file != null && line != null) listOf(frame(file, line, emptyList())) else emptyList()
        }
        if (frames.isEmpty()) return
        val stack = object : XExecutionStack("bettergo", null) {
            override fun getTopFrame(): XStackFrame? = frames.firstOrNull()
            override fun computeStackFrames(firstFrameIndex: Int, container: XExecutionStack.XStackFrameContainer) {
                container.addStackFrames(frames.drop(firstFrameIndex), true)
            }
        }
        val ctx = object : XSuspendContext() {
            override fun getActiveExecutionStack(): XExecutionStack = stack
        }
        ApplicationManager.getApplication().invokeLater {
            session.positionReached(ctx, true)
        }
    }

    private fun stackFrames(): List<XStackFrame> {
        val c = client ?: return emptyList()
        val reply = c.call("RPCServer.Stacktrace", mapOf("Id" to -1, "Depth" to 20, "Full" to true))
        val locs = reply.getAsJsonObject("result")?.getAsJsonArray("Locations") ?: return emptyList()
        val frames = mutableListOf<XStackFrame>()
        for (el in locs) {
            val obj = el.asJsonObject
            val loc = obj.getAsJsonObject("location") ?: obj.getAsJsonObject("Location") ?: obj
            val file = loc.get("file")?.asString ?: continue
            val line = loc.get("line")?.asInt ?: continue
            val locals = mutableListOf<Pair<String, String>>()
            for (key in listOf("locals", "Locals", "arguments", "Arguments")) {
                val arr = obj.getAsJsonArray(key) ?: continue
                for (v in arr) {
                    val vo = v.asJsonObject
                    locals += (vo.get("name")?.asString ?: "?") to (vo.get("value")?.asString ?: vo.get("Value")?.asString ?: "")
                }
            }
            frames += frame(file, line, locals)
        }
        return frames
    }

    private fun frame(file: String, line: Int, locals: List<Pair<String, String>>): XStackFrame {
        val vf = LocalFileSystem.getInstance().findFileByPath(file)
        val pos = if (vf != null) XDebuggerUtil.getInstance().createPosition(vf, (line - 1).coerceAtLeast(0)) else null
        return object : XStackFrame() {
            override fun getSourcePosition() = pos
            override fun computeChildren(node: XCompositeNode) {
                val list = XValueChildrenList()
                for ((n, v) in locals) {
                    list.add(n, object : XValue() {
                        override fun computePresentation(node: XValueNode, place: XValuePlace) {
                            node.setPresentation(null, null, v, false)
                        }
                    })
                }
                node.addChildren(list, true)
            }
        }
    }
}

class DelveClient private constructor(private val socket: Socket) {
    private val writer = OutputStreamWriter(socket.getOutputStream())
    private val lines = BufferedReader(InputStreamReader(socket.getInputStream()))
    private val gson = Gson()
    private var nextId = 0

    fun call(method: String, arg: Any): JsonObject {
        val id = ++nextId
        val req = JsonObject()
        req.addProperty("method", method)
        req.addProperty("id", id)
        val params = com.google.gson.JsonArray()
        params.add(gson.toJsonTree(arg))
        req.add("params", params)
        val line = synchronized(writer) {
            writer.write(req.toString())
            writer.write("\n")
            writer.flush()
            lines.readLine()
        } ?: throw ExecutionException("delve closed the connection")
        return JsonParser.parseString(line).asJsonObject
    }

    fun close() {
        try {
            socket.close()
        } catch (_: Exception) {
        }
    }

    companion object {
        fun connect(port: Int, process: java.lang.Process): DelveClient {
            val deadline = System.currentTimeMillis() + 60_000
            var last: Exception? = null
            while (System.currentTimeMillis() < deadline) {
                if (!process.isAlive) {
                    val output = process.inputStream.bufferedReader().readText().trim()
                    val message = output.lineSequence().firstOrNull { it.isNotBlank() }
                        ?: "delve exited before listening on $port"
                    throw ExecutionException(message, last)
                }
                try {
                    return DelveClient(Socket("127.0.0.1", port))
                } catch (e: Exception) {
                    last = e
                    Thread.sleep(100)
                }
            }
            throw ExecutionException("delve did not listen on $port", last)
        }
    }
}
