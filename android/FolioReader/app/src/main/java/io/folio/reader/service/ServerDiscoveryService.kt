package io.folio.reader.service

import android.content.Context
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import io.folio.reader.model.DiscoverResponse
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.CopyOnWriteArrayList

sealed class ConnectionState {
    data object Idle : ConnectionState()
    data object Discovering : ConnectionState()
    data class Connected(val baseURL: String, val via: String) : ConnectionState()
    data class Unavailable(val message: String) : ConnectionState()
}

class ServerDiscoveryService(
    private val context: Context,
    private val api: FolioAPIClient
) {
    private val prefs = context.getSharedPreferences("folio_discovery", Context.MODE_PRIVATE)
    private val savedUrlKey = "saved_base_url"

    private val defaultCandidates = listOf(
        "https://folio",
        "http://folio:8080",
        "http://folio:3000",
        "http://folio.local:8080"
    )

    private val _connectionState = MutableStateFlow<ConnectionState>(ConnectionState.Idle)
    val connectionState: StateFlow<ConnectionState> = _connectionState.asStateFlow()

    private val _activeBaseURL = MutableStateFlow(prefs.getString(savedUrlKey, null))
    val activeBaseURL: StateFlow<String?> = _activeBaseURL.asStateFlow()

    private val _serverInfo = MutableStateFlow<DiscoverResponse?>(null)
    val serverInfo: StateFlow<DiscoverResponse?> = _serverInfo.asStateFlow()

    private val nsdCandidates = CopyOnWriteArrayList<String>()
    private var nsdManager: NsdManager? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null
    private var multicastLock: WifiManager.MulticastLock? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    suspend fun discoverAutomatically() {
        _connectionState.value = ConnectionState.Discovering

        val candidates = linkedSetOf<String>()
        prefs.getString(savedUrlKey, null)?.takeIf { it.isNotBlank() }?.let { candidates.add(it) }
        candidates.addAll(defaultCandidates)
        candidates.addAll(nsdCandidates)

        startNsdBrowsing()

        val hit = raceProbe(candidates.toList())
        if (hit != null) {
            setActiveServer(hit.url, hit.info, hit.via)
            return
        }

        // Wait briefly for LAN mDNS (_folio._tcp) to resolve
        delay(1500)
        val nsdOnly = nsdCandidates.filter { it !in candidates }
        if (nsdOnly.isNotEmpty()) {
            val lanHit = raceProbe(nsdOnly)
            if (lanHit != null) {
                setActiveServer(lanHit.url, lanHit.info, "Local Wi-Fi (mDNS)")
                return
            }
        }

        val existing = _activeBaseURL.value
        _connectionState.value = if (existing != null) {
            val host = runCatching { Uri.parse(existing).host }.getOrNull() ?: "cached library"
            ConnectionState.Unavailable("Server unreachable (Offline mode using $host)")
        } else {
            ConnectionState.Unavailable("Could not find Folio on your Tailnet (folio) or LAN.")
        }
    }

    suspend fun connectManually(urlString: String): Boolean {
        var trimmed = urlString.trim()
        if (trimmed.isEmpty()) return false
        if (!trimmed.contains("://")) {
            trimmed = "https://$trimmed"
        }
        trimmed = trimmed.trimEnd('/')

        _connectionState.value = ConnectionState.Discovering
        val info = api.probeDiscover(trimmed, timeoutMs = 3500)
        if (info != null) {
            setActiveServer(trimmed, info, "Manual")
            return true
        }
        _connectionState.value = ConnectionState.Unavailable("No Folio server responded at $trimmed")
        return false
    }

    suspend fun handleDeepLink(uri: Uri?): Boolean {
        if (uri == null || !uri.scheme.equals("folio", ignoreCase = true)) return false
        val target = uri.getQueryParameter("url") ?: return false
        return connectManually(target)
    }

    private fun setActiveServer(url: String, info: DiscoverResponse, via: String) {
        _activeBaseURL.value = url
        _serverInfo.value = info
        prefs.edit().putString(savedUrlKey, url).apply()
        val label = if (info.authSource == "tailscale-serve" || info.authSource == "tailscaled-whois") {
            "Tailscale (${info.user ?: "Authenticated"})"
        } else {
            via
        }
        _connectionState.value = ConnectionState.Connected(baseURL = url, via = label)
    }

    private data class ProbeHit(val url: String, val info: DiscoverResponse, val via: String)

    private suspend fun raceProbe(candidates: List<String>): ProbeHit? = coroutineScope {
        val deferred = candidates.map { url ->
            async(Dispatchers.IO) {
                val info = api.probeDiscover(url, timeoutMs = 2000) ?: return@async null
                val host = runCatching { Uri.parse(url).host }.getOrNull().orEmpty()
                val via = if (host.contains("folio")) "Tailscale MagicDNS" else "Saved Server"
                ProbeHit(url, info, via)
            }
        }
        deferred.awaitAll().firstOrNull { it != null }
    }

    @Suppress("DEPRECATION")
    private fun startNsdBrowsing() {
        if (discoveryListener != null) return
        try {
            val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
            multicastLock = wifi?.createMulticastLock("folio-mdns")?.apply {
                setReferenceCounted(false)
                acquire()
            }

            val manager = context.getSystemService(Context.NSD_SERVICE) as? NsdManager ?: return
            nsdManager = manager

            val listener = object : NsdManager.DiscoveryListener {
                override fun onDiscoveryStarted(serviceType: String) {}
                override fun onDiscoveryStopped(serviceType: String) {}
                override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                    stopNsdBrowsing()
                }
                override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
                override fun onServiceLost(serviceInfo: NsdServiceInfo) {}
                override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                    try {
                        manager.resolveService(serviceInfo, object : NsdManager.ResolveListener {
                            override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {}
                            override fun onServiceResolved(resolved: NsdServiceInfo) {
                                val host = resolved.host?.hostAddress ?: return
                                val port = resolved.port
                                val url = if (host.contains(":")) "http://[$host]:$port" else "http://$host:$port"
                                if (!nsdCandidates.contains(url)) {
                                    nsdCandidates.add(url)
                                }
                            }
                        })
                    } catch (_: Exception) {}
                }
            }
            discoveryListener = listener
            manager.discoverServices("_folio._tcp.", NsdManager.PROTOCOL_DNS_SD, listener)
        } catch (_: Exception) {
            // mDNS optional
        }
    }

    fun stopNsdBrowsing() {
        try {
            discoveryListener?.let { nsdManager?.stopServiceDiscovery(it) }
        } catch (_: Exception) {}
        discoveryListener = null
        try {
            if (multicastLock?.isHeld == true) multicastLock?.release()
        } catch (_: Exception) {}
        multicastLock = null
    }
}
