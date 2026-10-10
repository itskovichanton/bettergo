package bettergo

import com.intellij.execution.ExecutionException
import com.intellij.execution.Executor
import com.intellij.openapi.components.PathMacroManager
import com.intellij.execution.configurations.CommandLineState
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeBase
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.configurations.RunConfigurationBase
import com.intellij.execution.configurations.RunConfigurationOptions
import com.intellij.execution.configurations.RunProfileState
import com.intellij.execution.process.KillableColoredProcessHandler
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.icons.AllIcons
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import org.jdom.Element
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel

class BetterGoOptions : RunConfigurationOptions()

class BetterGoRunConfigurationType : ConfigurationTypeBase(
    "BetterGo",
    "BetterGo",
    "Run or debug a BetterGo package",
    AllIcons.RunConfigurations.Application
) {
    init {
        addFactory(object : ConfigurationFactory(this) {
            override fun getId() = "BetterGo"
            override fun createTemplateConfiguration(project: Project): RunConfiguration =
                BetterGoRunConfiguration(project, this)
        })
    }
}

class BetterGoRunConfiguration(project: Project, factory: ConfigurationFactory) :
    RunConfigurationBase<BetterGoOptions>(project, factory, "BetterGo") {

    var packageDir: String = ""

    fun resolvedPackageDir(): String {
        if (packageDir.isBlank()) return ""
        return PathMacroManager.getInstance(project).expandPath(packageDir)
    }

    override fun readExternal(element: Element) {
        super.readExternal(element)
        packageDir = element.getAttributeValue("packageDir") ?: ""
    }

    override fun writeExternal(element: Element) {
        super.writeExternal(element)
        if (packageDir.isNotEmpty()) element.setAttribute("packageDir", packageDir)
    }

    override fun getState(executor: Executor, environment: ExecutionEnvironment): RunProfileState {
        return object : CommandLineState(environment) {
            override fun startProcess(): ProcessHandler {
                val dir = resolvedPackageDir().ifBlank { project.basePath ?: "." }
                if (!java.io.File(dir).isDirectory) {
                    throw ExecutionException("BetterGo package directory does not exist: $dir")
                }
                val pkg = if (resolvedPackageDir().isBlank()) "./example" else "."
                val cmd = GeneralCommandLine(Tools.executable("bettergo"), "run", pkg)
                cmd.withWorkDirectory(dir)
                Tools.enrichPath(cmd.environment)
                return KillableColoredProcessHandler(cmd)
            }
        }
    }

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> =
        object : SettingsEditor<BetterGoRunConfiguration>() {
            override fun resetEditorFrom(s: BetterGoRunConfiguration) {}
            override fun applyEditorTo(s: BetterGoRunConfiguration) {}
            override fun createEditor(): JComponent = JPanel().apply {
                add(JLabel("Runs bettergo run ./example in the project directory. bettergo must be on PATH."))
            }
        }
}
