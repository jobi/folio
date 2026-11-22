package io.folio.reader.service

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.LruCache
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/**
 * Two-tier (RAM LruCache + downsampled JPEG disk cache) cover image cache with in-flight deduplication.
 * Provides synchronous memory lookup so LazyVerticalGrid cells never flicker or reload when scrolling.
 */
class CoverImageCache(context: Context) {

    private val memoryCache: LruCache<Long, Bitmap> = object : LruCache<Long, Bitmap>(
        ((Runtime.getRuntime().maxMemory() / 1024).toInt() / 8).coerceIn(8 * 1024, 48 * 1024)
    ) {
        override fun sizeOf(key: Long, value: Bitmap): Int = (value.byteCount / 1024).coerceAtLeast(1)
    }

    private val diskDir: File = File(context.cacheDir, "FolioCovers").apply { mkdirs() }
    private val inFlight = ConcurrentHashMap<Long, CompletableDeferred<Bitmap?>>()

    private val client = OkHttpClient.Builder()
        .connectTimeout(12, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    fun cachedBitmap(bookID: Long): Bitmap? = memoryCache.get(bookID)

    suspend fun loadCover(bookID: Long, url: String?): Bitmap? {
        memoryCache.get(bookID)?.let { return it }

        val existing = inFlight[bookID]
        if (existing != null) {
            return existing.await()
        }

        val deferred = CompletableDeferred<Bitmap?>()
        val raced = inFlight.putIfAbsent(bookID, deferred)
        if (raced != null) {
            return raced.await()
        }

        val result = try {
            withContext(Dispatchers.IO) {
                val diskFile = File(diskDir, "$bookID.jpg")
                // 1. Check disk cache first (also works offline!)
                if (diskFile.exists()) {
                    val bmp = decodeDownsampled(diskFile.readBytes(), maxPx = 500)
                    if (bmp != null) {
                        memoryCache.put(bookID, bmp)
                        return@withContext bmp
                    }
                }

                // 2. Fetch from Folio server
                if (url.isNullOrBlank()) return@withContext null
                val req = Request.Builder()
                    .url(url)
                    .header("X-Folio-Client", "android")
                    .get()
                    .build()
                client.newCall(req).execute().use { resp ->
                    if (!resp.isSuccessful) return@withContext null
                    val bytes = resp.body?.bytes() ?: return@withContext null
                    val bmp = decodeDownsampled(bytes, maxPx = 500) ?: return@withContext null
                    try {
                        FileOutputStream(diskFile).use { fos ->
                            bmp.compress(Bitmap.CompressFormat.JPEG, 85, fos)
                        }
                    } catch (_: Exception) {}
                    memoryCache.put(bookID, bmp)
                    bmp
                }
            }
        } catch (_: Exception) {
            null
        } finally {
            inFlight.remove(bookID)
        }

        deferred.complete(result)
        return result
    }

    private fun decodeDownsampled(bytes: ByteArray, maxPx: Int): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null

        var sampleSize = 1
        val maxDim = maxOf(bounds.outWidth, bounds.outHeight)
        while (maxDim / sampleSize > maxPx * 2) {
            sampleSize *= 2
        }

        val opts = BitmapFactory.Options().apply {
            inSampleSize = sampleSize
            inPreferredConfig = Bitmap.Config.RGB_565
        }
        return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, opts)
    }
}
