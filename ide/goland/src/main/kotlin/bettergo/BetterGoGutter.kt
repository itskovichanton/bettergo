package bettergo

import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationType
import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.icons.AllIcons
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement

class BetterGoRunLineMarkerContributor : RunLineMarkerContributor() {
    override fun getInfo(element: PsiElement): Info? {
        if (!isFuncMainName(element)) return null
        return withExecutorActions(AllIcons.RunConfigurations.TestState.Run)
    }
}

class BetterGoRunConfigurationProducer : LazyRunConfigurationProducer<BetterGoRunConfiguration>() {
    override fun getConfigurationFactory(): ConfigurationFactory =
        ConfigurationType.CONFIGURATION_TYPE_EP.extensionList
            .filterIsInstance<BetterGoRunConfigurationType>()
            .first()
            .configurationFactories
            .first()

    override fun setupConfigurationFromContext(
        configuration: BetterGoRunConfiguration,
        context: ConfigurationContext,
        sourceElement: Ref<PsiElement>,
    ): Boolean {
        val element = sourceElement.get() ?: return false
        if (!isFuncMainName(element)) return false
        val dir = element.containingFile?.virtualFile?.parent ?: return false
        configuration.packageDir = dir.path
        configuration.name = "main"
        return true
    }

    override fun isConfigurationFromContext(
        configuration: BetterGoRunConfiguration,
        context: ConfigurationContext,
    ): Boolean {
        val element = context.psiLocation ?: return false
        if (!isFuncMainName(element)) return false
        val dir = element.containingFile?.virtualFile?.parent?.path ?: return false
        return configuration.packageDir == dir
    }
}
