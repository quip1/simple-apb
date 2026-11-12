// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.simpleapb.app.Preset
import org.simpleapb.app.ui.AppViewModel
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.account.Account
import org.simpleapb.core.ui.R as UiR

/** Servers and accounts on this device: switch, sign out, add another (brief §7.4). */
@Composable
fun AccountsScreen(app: AppViewModel, active: Account, accounts: List<Account>, onBack: () -> Unit) {
    FormColumn {
        ScreenHeader(stringResource(UiR.string.app_account_title), onBack = onBack)
        accounts.forEach { a ->
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("${a.displayName} (@${a.username})", style = MaterialTheme.typography.bodyLarge)
                Text(a.serverName + " · " + a.serverUrl, style = MaterialTheme.typography.bodySmall)
                if (a.id == active.id) {
                    Text(stringResource(UiR.string.app_account_active), style = MaterialTheme.typography.labelMedium)
                } else {
                    TextButton(onClick = { app.switchTo(a) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                        Text(stringResource(UiR.string.app_account_switch, a.displayName, a.serverName))
                    }
                }
                TextButton(onClick = { app.signOut(a) }, modifier = Modifier.heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.auth_sign_out))
                }
            }
            HorizontalDivider()
        }
        if (Preset.server == null) {
            OutlinedButton(onClick = app::startAdding, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.app_account_add))
            }
        }
    }
}

/**
 * The account must turn on two-factor sign-in before anything else
 * (brief §6.6). Shown instead of the app until it's done.
 */
@Composable
fun EnrollRequiredScreen(app: AppViewModel, account: Account) {
    val vm: SecurityViewModel = hiltViewModel()
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(account.id) { vm.startEnroll() }
    FormColumn {
        ScreenHeader(stringResource(UiR.string.totp_title))
        Notice(stringResource(UiR.string.totp_required), error = false)
        val e = s.enrollment
        if (e != null) {
            TotpSetup(e, s.codes, s.busy == SecurityForm.ENROLL, s.error?.second, vm::confirmEnroll, vm::finishEnroll)
        } else {
            s.error?.second?.let { Notice(errorText(it)) }
            BusyButton(stringResource(UiR.string.security_totp_enable), s.busy == SecurityForm.ENROLL, onClick = vm::startEnroll)
        }
        TextButton(onClick = {
            app.signOut(account)
        }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.auth_sign_out)) }
    }
}
