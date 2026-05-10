package com.nbregister.whatsappforwarder.receiver

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.nbregister.whatsappforwarder.worker.WorkerScheduler

class BootCompletedReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        val action = intent?.action ?: return
        if (action == Intent.ACTION_BOOT_COMPLETED || action == Intent.ACTION_LOCKED_BOOT_COMPLETED) {
            WorkerScheduler.ensurePeriodic(context)
            WorkerScheduler.enqueueImmediate(context)
        }
    }
}
