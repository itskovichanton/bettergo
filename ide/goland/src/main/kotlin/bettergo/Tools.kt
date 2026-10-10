package bettergo

import java.io.File

object Tools {
    fun executable(name: String): String {
        val home = System.getProperty("user.home")
        val candidates = listOf(
            "$home/go/bin/$name",
            "$home/sdk/go/bin/$name",
            "/opt/homebrew/bin/$name",
            "/usr/local/go/bin/$name",
            "/usr/local/bin/$name",
        )
        return candidates.firstOrNull { File(it).canExecute() } ?: name
    }

    /** GoLand started from the Dock does not see Homebrew, so dlv cannot find `go`. */
    fun enrichPath(env: MutableMap<String, String>) {
        val path = env["PATH"] ?: "/usr/bin:/bin:/usr/sbin:/sbin"
        val home = System.getProperty("user.home")
        val prefix = listOf(
            "$home/go/bin",
            "$home/sdk/go/bin",
            "/opt/homebrew/bin",
            "/usr/local/go/bin",
            "/usr/local/bin",
        ).filter { File(it).isDirectory }
        env["PATH"] = (prefix + path.split(":")).filter { it.isNotEmpty() }.distinct().joinToString(":")
    }
}
