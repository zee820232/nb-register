package com.nbregister.whatsappforwarder.data

import android.content.Context
import com.nbregister.whatsappforwarder.settings.SettingsStore
import java.util.UUID
import kotlin.math.pow

class QueueRepository(context: Context) {
    private val appContext = context.applicationContext
    private val dao = AppDatabase.getInstance(appContext).queueDao()
    private val settingsStore = SettingsStore(appContext)

    suspend fun enqueueCandidate(
        packageName: String,
        appName: String,
        title: String,
        text: String,
        postedAt: Long,
        notificationKey: String,
    ): Boolean {
        val settings = settingsStore.readAll()
        if (!settings.forwardingEnabled || packageName !in settings.whatsappPackages) {
            return false
        }

        val otp = OtpExtractor.extractOtp(text) ?: return false
        if (settings.requireKeyword && !OtpExtractor.hasKeyword(text, settings.keywords)) {
            return false
        }

        val now = System.currentTimeMillis()
        val item = OtpQueueItem(
            eventId = UUID.randomUUID().toString(),
            packageName = packageName,
            appName = appName,
            title = title.take(MAX_TEXT_LEN),
            text = text.take(MAX_TEXT_LEN),
            otp = otp,
            postedAt = postedAt,
            notificationKey = notificationKey.take(MAX_TEXT_LEN),
            nextRetryAt = now,
            createdAt = now,
            updatedAt = now,
        )
        dao.insert(item)
        return true
    }

    suspend fun getPending(limit: Int): List<OtpQueueItem> {
        val now = System.currentTimeMillis()
        dao.resetStaleSending(cutoff = now - STALE_SENDING_MS, now = now)
        return dao.getPending(now, limit)
    }

    suspend fun markSending(items: List<OtpQueueItem>) {
        if (items.isNotEmpty()) {
            dao.markSending(items.map { it.id }, System.currentTimeMillis())
        }
    }

    suspend fun markSent(item: OtpQueueItem) {
        dao.markSent(item.id, System.currentTimeMillis())
    }

    suspend fun markFailure(item: OtpQueueItem, maxRetries: Int, error: String, permanent: Boolean) {
        val nextAttempt = item.attemptCount + 1
        val exhausted = permanent || nextAttempt >= maxRetries
        val now = System.currentTimeMillis()
        val delay = if (exhausted) 0L else retryDelay(nextAttempt)
        dao.markFailure(
            id = item.id,
            status = if (exhausted) QueueStatus.FAILED else QueueStatus.PENDING,
            attemptCount = if (permanent) maxRetries else nextAttempt,
            nextRetryAt = now + delay,
            lastError = error.take(MAX_TEXT_LEN),
            updatedAt = now,
        )
    }

    suspend fun hasReadyPending(): Boolean {
        return dao.readyPendingCount(System.currentTimeMillis()) > 0
    }

    fun observeStats() = dao.observeStats()

    fun observeRecent(limit: Int) = dao.observeRecent(limit)

    suspend fun clearAll() {
        dao.clearAll()
    }

    suspend fun pruneOldSent() {
        dao.pruneOldSent(System.currentTimeMillis() - SENT_RETENTION_MS)
    }

    private fun retryDelay(attemptCount: Int): Long {
        val bounded = attemptCount.coerceIn(1, 6)
        val base = 15_000.0 * 2.0.pow((bounded - 1).toDouble())
        val jitter = (0..3_000).random()
        return base.toLong() + jitter
    }

    companion object {
        private const val MAX_TEXT_LEN = 2000
        private const val STALE_SENDING_MS = 10 * 60 * 1000L
        private const val SENT_RETENTION_MS = 7 * 24 * 60 * 60 * 1000L
    }
}
