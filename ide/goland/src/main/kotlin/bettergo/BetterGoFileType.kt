package bettergo

import com.intellij.lang.Language
import com.intellij.openapi.fileTypes.LanguageFileType
import com.intellij.openapi.util.IconLoader
import javax.swing.Icon

object BetterGoLanguage : Language("BetterGo")

class BetterGoFileType private constructor() : LanguageFileType(BetterGoLanguage) {
    override fun getName() = "BetterGo"
    override fun getDescription() = "BetterGo source"
    override fun getDefaultExtension() = "bgo"
    override fun getIcon(): Icon = IconLoader.getIcon("/icons/bettergo.svg", BetterGoFileType::class.java)

    companion object {
        @JvmField
        val INSTANCE = BetterGoFileType()
    }
}
