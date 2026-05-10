package com.nbregister.whatsappforwarder

import android.app.Application
import com.nbregister.whatsappforwarder.worker.WorkerScheduler

class WhatsappForwarderApp : Application() {
    override fun onCreate() {
        super.onCreate()
        WorkerScheduler.ensurePeriodic(this)
    }
}
