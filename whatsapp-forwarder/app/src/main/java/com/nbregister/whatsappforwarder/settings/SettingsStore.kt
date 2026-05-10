package com.nbregister.whatsappforwarder.settings

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit
import com.nbregister.whatsappforwarder.BuildConfig

data class AppSettings(
    val webhookUrl: String,
    val forwardingEnabled: Boolean,
    val requireKeyword: Boolean,
    val keywords: Set<String>,
    val whatsappPackages: Set<String>,
    val maxRetries: Int,
    val batchSize: Int,
)

class SettingsStore(context: Context) {
    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences("whatsapp_forwarder_settings", Context.MODE_PRIVATE)

    var webhookUrl: String
        get() = prefs.getString(KEY_WEBHOOK_URL, BuildConfig.DEFAULT_WEBHOOK_URL) ?: ""
        set(value) = prefs.edit { putString(KEY_WEBHOOK_URL, value.trim()) }

    var forwardingEnabled: Boolean
        get() = prefs.getBoolean(KEY_FORWARDING_ENABLED, true)
        set(value) = prefs.edit { putBoolean(KEY_FORWARDING_ENABLED, value) }

    var requireKeyword: Boolean
        get() = prefs.getBoolean(KEY_REQUIRE_KEYWORD, true)
        set(value) = prefs.edit { putBoolean(KEY_REQUIRE_KEYWORD, value) }

    var keywordsRaw: String
        get() = prefs.getString(KEY_KEYWORDS, DEFAULT_KEYWORDS) ?: DEFAULT_KEYWORDS
        set(value) = prefs.edit { putString(KEY_KEYWORDS, value) }

    var whatsappPackagesRaw: String
        get() = prefs.getString(KEY_WHATSAPP_PACKAGES, DEFAULT_WHATSAPP_PACKAGES) ?: DEFAULT_WHATSAPP_PACKAGES
        set(value) = prefs.edit { putString(KEY_WHATSAPP_PACKAGES, value) }

    var maxRetries: Int
        get() = prefs.getInt(KEY_MAX_RETRIES, 10)
        set(value) = prefs.edit { putInt(KEY_MAX_RETRIES, value.coerceIn(1, 30)) }

    var batchSize: Int
        get() = prefs.getInt(KEY_BATCH_SIZE, 20)
        set(value) = prefs.edit { putInt(KEY_BATCH_SIZE, value.coerceIn(1, 100)) }

    fun readAll(): AppSettings {
        return AppSettings(
            webhookUrl = webhookUrl,
            forwardingEnabled = forwardingEnabled,
            requireKeyword = requireKeyword,
            keywords = parseList(keywordsRaw).map { it.lowercase() }.toSet(),
            whatsappPackages = parseList(whatsappPackagesRaw),
            maxRetries = maxRetries,
            batchSize = batchSize,
        )
    }

    fun isWatchedPackage(packageName: String): Boolean {
        return parseList(whatsappPackagesRaw).contains(packageName)
    }

    companion object {
        const val DEFAULT_WHATSAPP_PACKAGES = "com.whatsapp\ncom.whatsapp.w4b"
        const val DEFAULT_KEYWORDS = "otp\ncode\nkode\nverification\nverifikasi\ngopay\ngojek\none-time\nsekali pakai"

        private const val KEY_WEBHOOK_URL = "webhook_url"
        private const val KEY_FORWARDING_ENABLED = "forwarding_enabled"
        private const val KEY_REQUIRE_KEYWORD = "require_keyword"
        private const val KEY_KEYWORDS = "keywords"
        private const val KEY_WHATSAPP_PACKAGES = "whatsapp_packages"
        private const val KEY_MAX_RETRIES = "max_retries"
        private const val KEY_BATCH_SIZE = "batch_size"

        fun parseList(raw: String): Set<String> {
            return raw.split(',', '\n', ';')
                .map { it.trim() }
                .filter { it.isNotEmpty() }
                .toSet()
        }
    }
}
