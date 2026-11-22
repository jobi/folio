package io.folio.reader.service

import android.content.Context
import android.os.Build
import android.provider.Settings
import io.folio.reader.model.Book
import io.folio.reader.model.BooksResponse
import io.folio.reader.model.DiscoverResponse
import io.folio.reader.model.PutProgressRequest
import io.folio.reader.model.ReadingProgress
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.FileOutputStream
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

class FolioAPIClient(private val context: Context) {

    val json = Json {
        ignoreUnknownKeys = true
        isLenient = true
        explicitNulls = false
    }

    private val client = OkHttpClient.Builder()
        .connectTimeout(12, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        .build()

    private val downloadClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(120, TimeUnit.SECONDS)
        .build()

    val deviceName: String
        get() {
            val model = Build.MODEL.orEmpty().trim()
            return if (model.isNotEmpty()) "Folio Android ($model)" else "Folio Android"
        }

    val deviceID: String
        get() = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
            ?.takeIf { it.isNotBlank() }
            ?: "folio-android-device"

    private fun joinUrl(baseURL: String, path: String): String {
        val base = baseURL.trimEnd('/')
        val clean = path.trimStart('/')
        return "$base/$clean"
    }

    suspend fun probeDiscover(baseURL: String, timeoutMs: Long = 2000L): DiscoverResponse? =
        withContext(Dispatchers.IO) {
            try {
                val probeClient = client.newBuilder()
                    .connectTimeout(timeoutMs, TimeUnit.MILLISECONDS)
                    .readTimeout(timeoutMs, TimeUnit.MILLISECONDS)
                    .build()
                val req = Request.Builder()
                    .url(joinUrl(baseURL, "api/discover"))
                    .header("X-Folio-Client", "android")
                    .get()
                    .build()
                probeClient.newCall(req).execute().use { resp ->
                    if (!resp.isSuccessful) return@withContext null
                    val body = resp.body?.string() ?: return@withContext null
                    val decoded = json.decodeFromString<DiscoverResponse>(body)
                    if (decoded.service == "folio") decoded else null
                }
            } catch (_: Exception) {
                null
            }
        }

    suspend fun fetchBooks(baseURL: String, query: String = "", status: String = "all"): List<Book> =
        withContext(Dispatchers.IO) {
            val params = mutableListOf("sort=updated")
            if (query.isNotBlank()) params.add("q=${URLEncoder.encode(query, "UTF-8")}")
            if (status != "all") params.add("status=${URLEncoder.encode(status, "UTF-8")}")
            val url = joinUrl(baseURL, "api/books") + "?" + params.joinToString("&")
            val req = Request.Builder()
                .url(url)
                .header("X-Folio-Client", "android")
                .get()
                .build()
            client.newCall(req).execute().use { resp ->
                val body = resp.body?.string().orEmpty()
                if (!resp.isSuccessful) {
                    error("Server error (${resp.code}): ${body.take(120)}")
                }
                json.decodeFromString<BooksResponse>(body).books
            }
        }

    suspend fun downloadEPUB(baseURL: String, bookID: Long, destFile: File): Unit =
        withContext(Dispatchers.IO) {
            val url = joinUrl(baseURL, "api/books/$bookID/download")
            val req = Request.Builder()
                .url(url)
                .header("X-Folio-Client", "android")
                .get()
                .build()
            downloadClient.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) {
                    error("Download failed (${resp.code})")
                }
                val stream = resp.body?.byteStream() ?: error("Empty download response")
                destFile.parentFile?.mkdirs()
                val tmp = File(destFile.parentFile, "${destFile.name}.part")
                FileOutputStream(tmp).use { out ->
                    stream.copyTo(out)
                }
                if (destFile.exists()) destFile.delete()
                tmp.renameTo(destFile)
            }
        }

    fun coverURL(baseURL: String, book: Book): String {
        val raw = book.coverURL
        if (!raw.isNullOrBlank()) {
            if (raw.startsWith("http://") || raw.startsWith("https://")) return raw
            return joinUrl(baseURL, raw)
        }
        return joinUrl(baseURL, "api/books/${book.id}/cover")
    }

    suspend fun pullProgress(baseURL: String, documentHash: String): ReadingProgress? =
        withContext(Dispatchers.IO) {
            if (documentHash.isBlank()) return@withContext null
            val url = joinUrl(baseURL, "syncs/progress/$documentHash")
            val req = Request.Builder()
                .url(url)
                .header("X-Folio-Client", "android")
                .get()
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return@withContext null
                val body = resp.body?.string()?.trim().orEmpty()
                if (body.isEmpty() || body == "{}") return@withContext null
                val decoded = json.decodeFromString<ReadingProgress>(body)
                if (decoded.document.isBlank() && decoded.percentage <= 0.0) null else decoded
            }
        }

    suspend fun pushProgress(baseURL: String, payload: PutProgressRequest): Unit =
        withContext(Dispatchers.IO) {
            val encoded = json.encodeToString(payload)
            val req = Request.Builder()
                .url(joinUrl(baseURL, "syncs/progress"))
                .header("X-Folio-Client", "android")
                .put(encoded.toRequestBody("application/json; charset=utf-8".toMediaType()))
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) {
                    error("Progress sync failed (${resp.code})")
                }
            }
        }
}
