package bettergo

import com.intellij.lexer.LexerBase
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.tree.IElementType

object BgoTokens {
    val KEYWORD = IElementType("BGO_KEYWORD", BetterGoLanguage)
    val COMMENT = IElementType("BGO_COMMENT", BetterGoLanguage)
    val STRING = IElementType("BGO_STRING", BetterGoLanguage)
    val NUMBER = IElementType("BGO_NUMBER", BetterGoLanguage)
    val BAD = IElementType("BGO_BAD", BetterGoLanguage)
}

private val keywords = setOf(
    "break", "case", "chan", "const", "continue", "default", "defer", "else",
    "fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
    "map", "package", "range", "return", "select", "struct", "switch", "type", "var"
)

class BetterGoLexer : LexerBase() {
    private var buf: CharSequence = ""
    private var end = 0
    private var cursor = 0
    private var tokStart = 0
    private var tokEnd = 0
    private var tok: IElementType? = null

    override fun start(buffer: CharSequence, startOffset: Int, endOffset: Int, initialState: Int) {
        buf = buffer
        end = endOffset
        cursor = startOffset
        advance()
    }

    override fun getState() = 0
    override fun getTokenType() = tok
    override fun getTokenStart() = tokStart
    override fun getTokenEnd() = tokEnd
    override fun getBufferSequence() = buf
    override fun getBufferEnd() = end

    override fun advance() {
        if (cursor >= end) {
            tok = null
            tokStart = cursor
            tokEnd = cursor
            return
        }
        tokStart = cursor
        val c = buf[cursor]
        when {
            c == '/' && cursor + 1 < end && buf[cursor + 1] == '/' -> {
                cursor += 2
                while (cursor < end && buf[cursor] != '\n') cursor++
                tok = BgoTokens.COMMENT
            }
            c == '/' && cursor + 1 < end && buf[cursor + 1] == '*' -> {
                cursor += 2
                while (cursor + 1 < end && !(buf[cursor] == '*' && buf[cursor + 1] == '/')) cursor++
                if (cursor + 1 < end) cursor += 2
                tok = BgoTokens.COMMENT
            }
            c == '"' || c == '\'' || c == '`' -> scanString(c)
            c.isWhitespace() -> {
                while (cursor < end && buf[cursor].isWhitespace()) cursor++
                tok = BgoTokens.BAD
                // whitespace is not a token we highlight; collapse it into a skipped token
                // by reporting it as an empty-looking keyword-less token. Highlighter ignores BAD if unmapped.
            }
            c.isLetter() || c == '_' -> {
                while (cursor < end && (buf[cursor].isLetterOrDigit() || buf[cursor] == '_')) cursor++
                val word = buf.subSequence(tokStart, cursor).toString()
                tok = if (word in keywords) BgoTokens.KEYWORD else BgoTokens.BAD
            }
            c.isDigit() -> {
                while (cursor < end && (buf[cursor].isLetterOrDigit() || buf[cursor] == '.' || buf[cursor] == '_')) cursor++
                tok = BgoTokens.NUMBER
            }
            else -> {
                cursor++
                tok = BgoTokens.BAD
            }
        }
        tokEnd = cursor
    }

    private fun scanString(quote: Char) {
        cursor++
        if (quote == '`') {
            while (cursor < end && buf[cursor] != '`') cursor++
        } else {
            while (cursor < end && buf[cursor] != quote && buf[cursor] != '\n') {
                if (buf[cursor] == '\\' && cursor + 1 < end) cursor += 2 else cursor++
            }
        }
        if (cursor < end && buf[cursor] == quote) cursor++
        tok = BgoTokens.STRING
    }
}

class BetterGoSyntaxHighlighter : SyntaxHighlighterBase() {
    override fun getHighlightingLexer() = BetterGoLexer()
    override fun getTokenHighlights(tokenType: IElementType?): Array<TextAttributesKey> {
        val key = when (tokenType) {
            BgoTokens.KEYWORD -> DefaultLanguageHighlighterColors.KEYWORD
            BgoTokens.COMMENT -> DefaultLanguageHighlighterColors.LINE_COMMENT
            BgoTokens.STRING -> DefaultLanguageHighlighterColors.STRING
            BgoTokens.NUMBER -> DefaultLanguageHighlighterColors.NUMBER
            else -> return TextAttributesKey.EMPTY_ARRAY
        }
        return arrayOf(key)
    }
}

class BetterGoSyntaxHighlighterFactory : SyntaxHighlighterFactory() {
    override fun getSyntaxHighlighter(project: Project?, virtualFile: VirtualFile?) = BetterGoSyntaxHighlighter()
}
