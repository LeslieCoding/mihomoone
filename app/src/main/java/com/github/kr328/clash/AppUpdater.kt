package com.github.kr328.clash

import android.content.Context
import android.content.Intent
import android.widget.Toast
import androidx.core.content.FileProvider
import com.github.kr328.clash.common.log.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.URL
import java.security.MessageDigest

object AppUpdater {
    private const val MANIFEST_URL =
        "https://github.com/LeslieCoding/mihomoone/releases/latest/download/latest.json"

    private val bootstrapProxies = listOf(
        InetSocketAddress("127.0.0.1", 7890),
        InetSocketAddress("192.168.5.6", 7897),
    )

    data class Release(
        val versionCode: Int,
        val versionName: String,
        val sha256: String,
    ) {
        val apkUrl: String
            get() = "https://github.com/LeslieCoding/mihomoone/releases/download/v$versionName/mihomoone-$versionName-alpha-arm64-v8a-release.apk"
    }

    private fun open(url: String): HttpURLConnection? {
        val candidates = listOf<Proxy>(Proxy.NO_PROXY) + bootstrapProxies.map {
            Proxy(Proxy.Type.HTTP, it)
        }

        for (proxy in candidates) {
            try {
                val conn = URL(url).openConnection(proxy) as HttpURLConnection
                conn.connectTimeout = 10_000
                conn.readTimeout = 30_000
                conn.instanceFollowRedirects = true
                conn.setRequestProperty("User-Agent", "MihomoOne-Updater")

                if (conn.responseCode in 200..299) return conn

                conn.disconnect()
            } catch (e: Exception) {
                Log.d("Updater open $url via $proxy: $e")
            }
        }

        return null
    }

    private fun currentVersionCode(context: Context): Long {
        val info = context.packageManager.getPackageInfo(context.packageName, 0)

        return if (android.os.Build.VERSION.SDK_INT >= 28)
            info.longVersionCode
        else
            @Suppress("DEPRECATION") info.versionCode.toLong()
    }

    private fun fetchManifest(): Release? {
        val conn = open(MANIFEST_URL) ?: return null

        return try {
            val json = JSONObject(conn.inputStream.bufferedReader().readText())

            Release(
                versionCode = json.getInt("versionCode"),
                versionName = json.getString("versionName"),
                sha256 = json.getString("sha256"),
            )
        } catch (e: Exception) {
            Log.w("Updater parse manifest: $e", e)
            null
        } finally {
            conn.disconnect()
        }
    }

    private fun sha256Of(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")

        file.inputStream().use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                digest.update(buffer, 0, read)
            }
        }

        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    private fun download(context: Context, release: Release): File? {
        val target = File(context.cacheDir, "mihomoone-update.apk")
        val conn = open(release.apkUrl) ?: return null

        try {
            conn.inputStream.use { input ->
                target.outputStream().use { output -> input.copyTo(output) }
            }
        } catch (e: Exception) {
            Log.w("Updater download: $e", e)
            target.delete()
            return null
        } finally {
            conn.disconnect()
        }

        if (!sha256Of(target).equals(release.sha256, ignoreCase = true)) {
            Log.w("Updater sha256 mismatch")
            target.delete()
            return null
        }

        return target
    }

    private fun install(context: Context, apk: File) {
        val uri = FileProvider.getUriForFile(
            context,
            "${context.packageName}.update_provider",
            apk,
        )

        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

        context.startActivity(intent)
    }

    suspend fun checkAndUpdate(context: Context) {
        val release = withContext(Dispatchers.IO) { fetchManifest() } ?: return

        if (release.versionCode <= currentVersionCode(context)) return

        withContext(Dispatchers.Main) {
            Toast.makeText(
                context,
                context.getString(com.github.kr328.clash.design.R.string.update_downloading, release.versionName),
                Toast.LENGTH_SHORT,
            ).show()
        }

        val apk = withContext(Dispatchers.IO) { download(context, release) }

        withContext(Dispatchers.Main) {
            if (apk == null) {
                Toast.makeText(
                    context,
                    com.github.kr328.clash.design.R.string.update_failed,
                    Toast.LENGTH_LONG,
                ).show()
                return@withContext
            }

            androidx.appcompat.app.AlertDialog.Builder(context)
                .setTitle(com.github.kr328.clash.design.R.string.update_found_title)
                .setMessage(
                    context.getString(
                        com.github.kr328.clash.design.R.string.update_found_message,
                        release.versionName,
                    )
                )
                .setPositiveButton(com.github.kr328.clash.design.R.string.update_now) { _, _ ->
                    install(context, apk)
                }
                .setNegativeButton(com.github.kr328.clash.design.R.string.update_later, null)
                .show()
        }
    }
}
