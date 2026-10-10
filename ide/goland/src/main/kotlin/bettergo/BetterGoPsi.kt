package bettergo

import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.project.Project
import com.intellij.extapi.psi.ASTWrapperPsiElement
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet
import com.intellij.psi.util.elementType

private val FILE = IFileElementType(BetterGoLanguage)

class BetterGoFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, BetterGoLanguage) {
    override fun getFileType() = BetterGoFileType.INSTANCE
}

class BetterGoParserDefinition : ParserDefinition {
    override fun createLexer(project: Project): Lexer = BetterGoLexer()

    override fun createParser(project: Project): PsiParser = PsiParser { root, builder ->
        val mark = builder.mark()
        while (!builder.eof()) builder.advanceLexer()
        mark.done(root)
        builder.treeBuilt
    }

    override fun getFileNodeType(): IFileElementType = FILE
    override fun getCommentTokens(): TokenSet = TokenSet.create(BgoTokens.COMMENT)
    override fun getStringLiteralElements(): TokenSet = TokenSet.create(BgoTokens.STRING)
    override fun createElement(node: ASTNode): PsiElement = ASTWrapperPsiElement(node)
    override fun createFile(viewProvider: FileViewProvider): PsiFile = BetterGoFile(viewProvider)
}

fun isFuncMainName(element: PsiElement): Boolean {
    if (element.elementType == BgoTokens.COMMENT || element.elementType == BgoTokens.STRING) return false
    if (element.text != "main") return false
    val prev = significantSibling(element, -1) ?: return false
    if (prev.text != "func") return false
    val next = significantSibling(element, 1) ?: return false
    return next.text.startsWith("(")
}

private fun significantSibling(start: PsiElement, direction: Int): PsiElement? {
    var el = if (direction < 0) start.prevSibling else start.nextSibling
    while (el != null && el.text.isBlank()) {
        el = if (direction < 0) el.prevSibling else el.nextSibling
    }
    return el
}
