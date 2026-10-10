package bettergo

import com.intellij.ide.projectView.ProjectView
import com.intellij.ide.projectView.ProjectViewNode
import com.intellij.ide.projectView.TreeStructureProvider
import com.intellij.ide.projectView.ViewSettings
import com.intellij.ide.util.PropertiesComponent
import com.intellij.ide.util.treeView.AbstractTreeNode
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.ToggleAction
import com.intellij.openapi.project.Project

private const val HIDE_GENERATED_KEY = "bettergo.hide.generated.go"

fun Project.hideGeneratedGo(): Boolean =
    PropertiesComponent.getInstance(this).getBoolean(HIDE_GENERATED_KEY, true)

fun Project.setHideGeneratedGo(hide: Boolean) {
    PropertiesComponent.getInstance(this).setValue(HIDE_GENERATED_KEY, hide, true)
    ProjectView.getInstance(this).refresh()
}

class HideGeneratedGoAction : ToggleAction() {
    override fun isSelected(e: AnActionEvent): Boolean = e.project?.hideGeneratedGo() == true

    override fun setSelected(e: AnActionEvent, state: Boolean) {
        e.project?.setHideGeneratedGo(state)
    }
}

class BetterGoTreeStructureProvider : TreeStructureProvider {
    override fun isDumbAware() = true

    override fun modify(
        parent: AbstractTreeNode<*>,
        children: Collection<AbstractTreeNode<*>>,
        settings: ViewSettings,
    ): Collection<AbstractTreeNode<*>> {
        val project = parent.project ?: return children
        if (!project.hideGeneratedGo()) return children
        return children.filterNot { isGeneratedGo(it) }
    }
}

private fun isGeneratedGo(node: AbstractTreeNode<*>): Boolean {
    val file = (node as? ProjectViewNode<*>)?.virtualFile ?: return false
    if (!file.isValid || file.isDirectory || file.extension != "go") return false
    val sibling = file.parent?.findChild(file.name.removeSuffix(".go") + ".bgo") ?: return false
    return !sibling.isDirectory
}
