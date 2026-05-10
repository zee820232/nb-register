package com.nbregister.whatsappforwarder.data

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

@Dao
interface QueueDao {
    @Insert
    suspend fun insert(item: OtpQueueItem): Long

    @Query(
        """
        SELECT * FROM otp_queue
        WHERE status = 'PENDING' AND nextRetryAt <= :now
        ORDER BY createdAt ASC
        LIMIT :limit
        """,
    )
    suspend fun getPending(now: Long, limit: Int): List<OtpQueueItem>

    @Query("UPDATE otp_queue SET status = 'SENDING', updatedAt = :now WHERE id IN (:ids)")
    suspend fun markSending(ids: List<Long>, now: Long)

    @Query(
        """
        UPDATE otp_queue
        SET status = 'SENT', lastError = NULL, updatedAt = :now
        WHERE id = :id
        """,
    )
    suspend fun markSent(id: Long, now: Long)

    @Query(
        """
        UPDATE otp_queue
        SET status = :status,
            attemptCount = :attemptCount,
            nextRetryAt = :nextRetryAt,
            lastError = :lastError,
            updatedAt = :updatedAt
        WHERE id = :id
        """,
    )
    suspend fun markFailure(
        id: Long,
        status: String,
        attemptCount: Int,
        nextRetryAt: Long,
        lastError: String,
        updatedAt: Long,
    )

    @Query(
        """
        UPDATE otp_queue
        SET status = 'PENDING', nextRetryAt = :now, updatedAt = :now
        WHERE status = 'SENDING' AND updatedAt < :cutoff
        """,
    )
    suspend fun resetStaleSending(cutoff: Long, now: Long)

    @Query("SELECT COUNT(*) FROM otp_queue WHERE status = 'PENDING' AND nextRetryAt <= :now")
    suspend fun readyPendingCount(now: Long): Int

    @Query(
        """
        SELECT
            COALESCE(SUM(CASE WHEN status = 'PENDING' THEN 1 ELSE 0 END), 0) AS pendingCount,
            COALESCE(SUM(CASE WHEN status = 'SENDING' THEN 1 ELSE 0 END), 0) AS sendingCount,
            COALESCE(SUM(CASE WHEN status = 'SENT' THEN 1 ELSE 0 END), 0) AS sentCount,
            COALESCE(SUM(CASE WHEN status = 'FAILED' THEN 1 ELSE 0 END), 0) AS failedCount
        FROM otp_queue
        """,
    )
    fun observeStats(): Flow<QueueStats>

    @Query("SELECT * FROM otp_queue ORDER BY createdAt DESC LIMIT :limit")
    fun observeRecent(limit: Int): Flow<List<OtpQueueItem>>

    @Query("DELETE FROM otp_queue")
    suspend fun clearAll()

    @Query("DELETE FROM otp_queue WHERE status = 'SENT' AND createdAt < :cutoff")
    suspend fun pruneOldSent(cutoff: Long)
}
