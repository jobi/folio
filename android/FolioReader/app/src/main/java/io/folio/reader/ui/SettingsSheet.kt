package io.folio.reader.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.folio.reader.model.FolioAccentColor
import io.folio.reader.model.ReaderSettings
import io.folio.reader.service.ConnectionState
import io.folio.reader.service.OfflineLibraryStore
import io.folio.reader.service.ServerDiscoveryService
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsSheet(
    discovery: ServerDiscoveryService,
    store: OfflineLibraryStore,
    readerSettings: ReaderSettings,
    onUpdateReaderSettings: (ReaderSettings) -> Unit,
    onDismiss: () -> Unit
) {
    val connectionState by discovery.connectionState.collectAsState()
    val serverInfo by discovery.serverInfo.collectAsState()
    val baseURL by discovery.activeBaseURL.collectAsState()
    val downloadedIDs by store.downloadedBookIDs.collectAsState()
    val outbox by store.pendingSyncOutbox.collectAsState()

    var manualURL by remember { mutableStateOf(baseURL.orEmpty()) }
    var isConnecting by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 8.dp)
        ) {
            Text("Settings", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Spacer(Modifier.height(14.dp))

            Text("Server Connection", style = MaterialTheme.typography.titleSmall, color = FolioAccentColor)
            Spacer(Modifier.height(6.dp))

            val statusText = when (val s = connectionState) {
                is ConnectionState.Connected -> "${s.via} (${s.baseURL})"
                is ConnectionState.Discovering -> "Searching..."
                is ConnectionState.Unavailable -> s.message
                is ConnectionState.Idle -> "Idle"
            }
            Text(statusText, style = MaterialTheme.typography.bodyMedium)

            serverInfo?.let { info ->
                if (!info.user.isNullOrBlank()) {
                    Text("Tailscale User: ${info.user}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (!info.tailnet.isNullOrBlank()) {
                    Text("Tailnet: ${info.tailnet}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }

            Spacer(Modifier.height(10.dp))
            OutlinedButton(
                onClick = {
                    scope.launch {
                        discovery.discoverAutomatically()
                        store.refreshLibrary(discovery.activeBaseURL.value)
                    }
                },
                modifier = Modifier.fillMaxWidth()
            ) {
                Text("Re-Scan Tailnet & Local Wi-Fi")
            }

            Spacer(Modifier.height(12.dp))
            OutlinedTextField(
                value = manualURL,
                onValueChange = { manualURL = it },
                label = { Text("Custom Server URL") },
                placeholder = { Text("https://folio or http://192.168.1.x:8080") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth()
            )
            Spacer(Modifier.height(8.dp))
            Button(
                onClick = {
                    scope.launch {
                        isConnecting = true
                        val ok = discovery.connectManually(manualURL)
                        if (ok) {
                            store.refreshLibrary(discovery.activeBaseURL.value)
                        }
                        isConnecting = false
                    }
                },
                enabled = manualURL.isNotBlank() && !isConnecting,
                colors = ButtonDefaults.buttonColors(containerColor = FolioAccentColor),
                modifier = Modifier.fillMaxWidth()
            ) {
                if (isConnecting) {
                    CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                } else {
                    Text("Connect")
                }
            }

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(14.dp))

            Text("Hardware & E-Ink Controls", style = MaterialTheme.typography.titleSmall, color = FolioAccentColor)
            Spacer(Modifier.height(8.dp))

            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text("Volume Buttons Turn Pages", style = MaterialTheme.typography.bodyMedium)
                    Text(
                        "Volume Down = Next, Volume Up = Previous",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Switch(
                    checked = readerSettings.volumeButtonsTurnPages,
                    onCheckedChange = {
                        onUpdateReaderSettings(readerSettings.copy(volumeButtonsTurnPages = it))
                    }
                )
            }

            if (readerSettings.volumeButtonsTurnPages) {
                Spacer(Modifier.height(8.dp))
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Swap Volume Up / Down", style = MaterialTheme.typography.bodyMedium)
                    Switch(
                        checked = readerSettings.swapVolumeButtons,
                        onCheckedChange = {
                            onUpdateReaderSettings(readerSettings.copy(swapVolumeButtons = it))
                        }
                    )
                }
            }

            Spacer(Modifier.height(8.dp))
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text("Animate Page Turns", style = MaterialTheme.typography.bodyMedium)
                    Text(
                        "Turn off on E-Ink (Boox Palma) for zero ghosting",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Switch(
                    checked = readerSettings.animatePageTurns,
                    onCheckedChange = {
                        onUpdateReaderSettings(readerSettings.copy(animatePageTurns = it))
                    }
                )
            }

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(14.dp))

            Text("Offline Storage & Sync Queue", style = MaterialTheme.typography.titleSmall, color = FolioAccentColor)
            Spacer(Modifier.height(6.dp))
            Text("Downloaded Books: ${downloadedIDs.size}", style = MaterialTheme.typography.bodyMedium)
            Text("Pending Sync Events: ${outbox.size}", style = MaterialTheme.typography.bodyMedium)

            if (outbox.isNotEmpty() && !baseURL.isNullOrBlank()) {
                Spacer(Modifier.height(8.dp))
                OutlinedButton(
                    onClick = { scope.launch { store.flushSyncOutbox(baseURL!!) } },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Flush Sync Queue Now")
                }
            }

            Spacer(Modifier.height(28.dp))
        }
    }
}
