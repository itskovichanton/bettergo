package bettergo

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

class BetterGoLspProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerSupportProvider.LspServerStarter,
    ) {
        if (file.extension == "bgo") {
            serverStarter.ensureServerStarted(BetterGoLspServer(project))
        }
    }
}

private class BetterGoLspServer(project: Project) : ProjectWideLspServerDescriptor(project, "BetterGo") {
    override fun isSupportedFile(file: VirtualFile) = file.extension == "bgo"

    override fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine(Tools.executable("bettergo"), "lsp")
}
