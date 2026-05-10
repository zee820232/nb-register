package com.nbregister.whatsappforwarder.data

import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey

object QueueStatus {
    const val PENDING = "PENDING"
    const val SENDING = "SENDING"
    const val SENT = "SENT"
    const val FAILED = "FAILED"
}

@Entity(
    tableName = "otp_queue",
    indices = [
        Index(value = ["status", "nextRetryAt"]),
        Index(value = ["createdAt"]),
    ],
)
data class OtpQueueItem(
    @PrimaryKey(autoGenerate = true)
    val id: Long = 0,
    val eventId: String,
    val packageName: String,
    val appName: String,
    val title: String,
    val text: String,
    val otp: String,
    val postedAt: Long,
    val notificationKey: String,
    val status: String = QueueStatus.PENDING,
    val attemptCount: Int = 0,
    val nextRetryAt: Long = 0,
    val lastError: String? = null,
    val createdAt: Long = System.currentTimeMillis(),
    val updatedAt: Long = System.currentTimeMillis(),
)

data class QueueStats(
    val pendingCount: Int = 0,
    val sendingCount: Int = 0,
    val sentCount: Int = 0,
    val failedCount: Int = 0,
)
