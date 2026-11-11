// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.push

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.core.content.ContextCompat
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.StateFlow
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.RadioGroup
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.core.ui.R as UiR

@HiltViewModel
class NotificationsViewModel
@Inject
constructor(private val push: PushManager) : ViewModel() {
    val status: StateFlow<PushStatus> = push.status

    fun refresh() = push.refresh()

    fun distributors() = push.distributors().map { it to push.label(it) }

    fun use(pkg: String) = push.use(pkg)

    fun stop() = push.stop()
}

/**
 * Notifications (brief §6.14, §7.4 screen 8 "push (distributor status)"):
 * whether push works through a UnifiedPush distributor, the choice of
 * distributor, and what happens without one.
 */
@Composable
fun NotificationsScreen(onBack: () -> Unit) {
    val vm: NotificationsViewModel = hiltViewModel()
    val status by vm.status.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var allowed by remember {
        mutableStateOf(
            Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
                ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        )
    }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { allowed = it }
    LaunchedEffect(Unit) { vm.refresh() }
    FormColumn {
        ScreenHeader(stringResource(UiR.string.app_push_title), onBack = onBack)
        // Android 13+ asks; older versions allow notifications by default.
        if (!allowed && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            Notice(stringResource(UiR.string.app_push_not_allowed))
            OutlinedButton(onClick = {
                ask.launch(Manifest.permission.POST_NOTIFICATIONS)
            }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_push_allow))
            }
        }
        Section(stringResource(UiR.string.app_push_how)) {
            when (val s = status) {
                is PushStatus.Active -> {
                    Text(stringResource(UiR.string.app_push_active, s.distributor))
                    TextButton(onClick = {
                        vm.stop()
                    }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.app_push_stop)) }
                }
                PushStatus.Choose -> Text(stringResource(UiR.string.app_push_choose))
                PushStatus.Fallback -> Text(stringResource(UiR.string.app_push_fallback))
            }
            val options = vm.distributors()
            if (options.isNotEmpty() && status !is PushStatus.Active) {
                RadioGroup<Pair<String, String>?>(null, options, { it?.second.orEmpty() }) { it?.let { p -> vm.use(p.first) } }
            }
            Text(stringResource(UiR.string.app_push_tradeoff), style = MaterialTheme.typography.bodySmall)
        }
    }
}
