// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.settings

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.model.Me
import org.simpleapb.api.model.PasswordChange
import org.simpleapb.api.model.Session
import org.simpleapb.api.model.TotpCode
import org.simpleapb.api.model.TotpConfirmRequest
import org.simpleapb.api.model.TotpDisable
import org.simpleapb.api.model.TotpEnrollment
import org.simpleapb.app.book.ago
import org.simpleapb.app.data.MeSession
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.ScreenHeader
import org.simpleapb.app.ui.Section
import org.simpleapb.app.ui.SectionTitle
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.call
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.ui.R as UiR

/** Which form a result belongs to, so errors show next to it. */
enum class SecurityForm { PASSWORD, ENROLL, CODES, DISABLE, DEVICES }

data class SecurityState(
    val busy: SecurityForm? = null,
    val error: Pair<SecurityForm, ApiFailure>? = null,
    val passwordChanged: Boolean = false,
    val enrollment: TotpEnrollment? = null,
    val codes: List<String> = emptyList(),
    val sessions: List<Session> = emptyList()
)

@HiltViewModel
class SecurityViewModel
@Inject
constructor(private val session: MeSession) : ViewModel() {
    val me: StateFlow<Me?> = session.me
    private val _state = MutableStateFlow(SecurityState())
    val state: StateFlow<SecurityState> = _state.asStateFlow()

    private suspend fun <T> run(form: SecurityForm, block: suspend (org.simpleapb.core.data.net.Api) -> Outcome<T>): T? {
        val api = session.api() ?: return null
        _state.update { it.copy(busy = form, error = null) }
        val r = block(api)
        _state.update { it.copy(busy = null, error = (r as? Outcome.Failed)?.let { f -> form to f.failure }) }
        return (r as? Outcome.Ok)?.value
    }

    fun loadSessions() = viewModelScope.launch {
        run(SecurityForm.DEVICES) { call { it.me.listMySessions() } }?.let { list -> _state.update { it.copy(sessions = list) } }
    }

    fun changePassword(current: String, new: String) = viewModelScope.launch {
        _state.update { it.copy(passwordChanged = false) }
        if (run(SecurityForm.PASSWORD) { call { it.me.changeMyPassword(PasswordChange(current, new)) } } != null) {
            _state.update { it.copy(passwordChanged = true) }
            loadSessions()
        }
    }

    fun startEnroll() = viewModelScope.launch {
        run(SecurityForm.ENROLL) { call { it.auth.enrollTotp() } }?.let { e -> _state.update { it.copy(enrollment = e, codes = emptyList()) } }
    }

    fun confirmEnroll(code: String) = viewModelScope.launch {
        run(SecurityForm.ENROLL) { call { it.auth.confirmTotp(TotpConfirmRequest(code.filter(Char::isDigit))) } }?.let { r ->
            _state.update { it.copy(codes = r.codes) }
        }
    }

    /** After the user has saved the recovery codes. */
    fun finishEnroll() = viewModelScope.launch {
        session.refresh()
        _state.update { it.copy(enrollment = null, codes = emptyList()) }
    }

    fun newCodes(code: String) = viewModelScope.launch {
        run(SecurityForm.CODES) { call { it.me.regenerateRecoveryCodes(TotpCode(code.filter(Char::isDigit))) } }?.let { r ->
            _state.update { it.copy(codes = r.codes) }
        }
    }

    fun disable(password: String, code: String) = viewModelScope.launch {
        if (run(SecurityForm.DISABLE) { call { it.me.disableMyTotp(TotpDisable(password, code.filter(Char::isDigit))) } } != null) {
            _state.update { it.copy(codes = emptyList()) }
            session.refresh()
        }
    }

    fun revoke(s: Session) = viewModelScope.launch {
        run(SecurityForm.DEVICES) { call { it.me.revokeMySession(s.id) } }
        loadSessions()
    }

    fun revokeOthers() = viewModelScope.launch {
        run(SecurityForm.DEVICES) { call { it.me.revokeMyOtherSessions() } }
        loadSessions()
    }
}

/** Password, two-factor sign-in, recovery codes and signed-in devices. */
@Composable
fun SecurityScreen(onBack: () -> Unit) {
    val vm: SecurityViewModel = hiltViewModel()
    val me by vm.me.collectAsStateWithLifecycle()
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { vm.loadSessions() }
    fun err(f: SecurityForm) = s.error?.takeIf { it.first == f }?.second
    FormColumn {
        ScreenHeader(stringResource(UiR.string.security_title), onBack = onBack)

        Section(stringResource(UiR.string.security_password)) {
            var current by rememberSaveable { mutableStateOf("") }
            var next by rememberSaveable { mutableStateOf("") }
            var repeat by rememberSaveable { mutableStateOf("") }
            var mismatch by rememberSaveable { mutableStateOf(false) }
            Password(stringResource(UiR.string.security_current_password), current) { current = it }
            Password(stringResource(UiR.string.security_new_password), next, hint = stringResource(UiR.string.auth_password_hint)) { next = it }
            Password(stringResource(UiR.string.security_repeat_password), repeat) { repeat = it }
            if (mismatch) Notice(stringResource(UiR.string.security_password_mismatch))
            err(SecurityForm.PASSWORD)?.let { Notice(errorText(it)) }
            if (s.passwordChanged) Notice(stringResource(UiR.string.security_password_changed), error = false)
            BusyButton(
                stringResource(UiR.string.security_change_password),
                s.busy == SecurityForm.PASSWORD,
                onClick = {
                    mismatch = next != repeat
                    if (!mismatch) {
                        vm.changePassword(current, next)
                        current = ""
                        next = ""
                        repeat = ""
                    }
                },
                enabled = current.isNotEmpty() && next.isNotEmpty() && repeat.isNotEmpty()
            )
        }

        Section(stringResource(UiR.string.security_totp)) {
            val m = me
            when {
                m == null -> Text(stringResource(UiR.string.common_loading))
                s.enrollment != null -> TotpSetup(
                    s.enrollment!!,
                    s.codes,
                    s.busy == SecurityForm.ENROLL,
                    err(SecurityForm.ENROLL),
                    vm::confirmEnroll,
                    vm::finishEnroll
                )
                m.totpEnabled -> TotpOn(m, s, vm, ::err)
                else -> {
                    Text(stringResource(UiR.string.security_totp_off))
                    err(SecurityForm.ENROLL)?.let { Notice(errorText(it)) }
                    BusyButton(stringResource(UiR.string.security_totp_enable), s.busy == SecurityForm.ENROLL, onClick = vm::startEnroll)
                }
            }
        }

        Section(stringResource(UiR.string.security_devices)) {
            err(SecurityForm.DEVICES)?.let { Notice(errorText(it)) }
            s.sessions.forEach { d ->
                Row(Modifier.fillMaxWidth()) {
                    Column(Modifier.weight(1f)) {
                        Text(d.deviceName, style = MaterialTheme.typography.bodyLarge)
                        if (d.current) Text(stringResource(UiR.string.security_this_device), style = MaterialTheme.typography.labelMedium)
                        Text(
                            stringResource(UiR.string.security_device_meta, d.platform.value, ago(d.lastSeenAt)),
                            style = MaterialTheme.typography.bodySmall
                        )
                    }
                    if (!d.current) {
                        TextButton(onClick = {
                            vm.revoke(d)
                        }, modifier = Modifier.heightIn(min = TouchTarget)) { Text(stringResource(UiR.string.security_sign_out_device)) }
                    }
                }
                HorizontalDivider()
            }
            if (s.sessions.any { !it.current }) {
                OutlinedButton(onClick = vm::revokeOthers, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.security_sign_out_others))
                }
            }
        }
    }
}

@Composable
private fun TotpOn(m: Me, s: SecurityState, vm: SecurityViewModel, err: (SecurityForm) -> ApiFailure?) {
    Text(stringResource(UiR.string.security_totp_on))
    SectionTitle(stringResource(UiR.string.security_recovery_codes))
    if (s.codes.isNotEmpty()) {
        RecoveryCodes(s.codes)
    } else {
        var code by rememberSaveable { mutableStateOf("") }
        Text(stringResource(UiR.string.security_recovery_help))
        Code(code) { code = it }
        err(SecurityForm.CODES)?.let { Notice(errorText(it)) }
        BusyButton(stringResource(UiR.string.security_new_codes), s.busy == SecurityForm.CODES, onClick = {
            vm.newCodes(code)
            code = ""
        }, enabled = code.length >= 6)
    }
    SectionTitle(stringResource(UiR.string.security_totp_off_title))
    if (m.totpRequired) {
        Text(stringResource(UiR.string.security_totp_required))
    } else {
        var password by rememberSaveable { mutableStateOf("") }
        var code by rememberSaveable { mutableStateOf("") }
        Password(stringResource(UiR.string.security_current_password), password) { password = it }
        Code(code) { code = it }
        err(SecurityForm.DISABLE)?.let { Notice(errorText(it)) }
        BusyButton(stringResource(UiR.string.security_totp_disable), s.busy == SecurityForm.DISABLE, onClick = {
            vm.disable(password, code)
            password = ""
            code = ""
        }, enabled = password.isNotEmpty() && code.length >= 6)
    }
}

/**
 * Two-factor enrolment on the phone itself: there is no second screen to
 * scan a QR code from, so the app offers the authenticator link and the
 * secret to copy, then asks for a code and shows the recovery codes once.
 */
@Composable
fun TotpSetup(e: TotpEnrollment, codes: List<String>, busy: Boolean, error: ApiFailure?, onConfirm: (String) -> Unit, onDone: () -> Unit) {
    if (codes.isNotEmpty()) {
        RecoveryCodes(codes)
        BusyButton(stringResource(UiR.string.common_continue), false, onClick = onDone)
        return
    }
    val context = LocalContext.current
    var noApp by rememberSaveable { mutableStateOf(false) }
    var code by rememberSaveable { mutableStateOf("") }
    Text(stringResource(UiR.string.app_totp_help))
    OutlinedButton(
        onClick = {
            try {
                context.startActivity(Intent(Intent.ACTION_VIEW, e.otpauthUri.toUri()))
            } catch (_: ActivityNotFoundException) {
                noApp = true
            }
        },
        modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
    ) { Text(stringResource(UiR.string.app_totp_open_app)) }
    if (noApp) Notice(stringResource(UiR.string.app_totp_no_app))
    Text(stringResource(UiR.string.setup_admin_secret), style = MaterialTheme.typography.labelMedium)
    SelectionContainer {
        Text(e.secret.chunked(4).joinToString(" "), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleMedium)
    }
    CopyButton(e.secret, stringResource(UiR.string.app_totp_copy_key))
    Code(code) { code = it }
    error?.let { Notice(errorText(it)) }
    BusyButton(stringResource(UiR.string.totp_enable), busy, onClick = { onConfirm(code) }, enabled = code.length >= 6)
}

@Composable
fun RecoveryCodes(codes: List<String>) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(stringResource(UiR.string.app_totp_codes_help))
        SelectionContainer {
            Column { codes.forEach { Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleMedium) } }
        }
        CopyButton(codes.joinToString("\n"), stringResource(UiR.string.security_codes_copy))
    }
}

@Composable
private fun CopyButton(text: String, label: String) {
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    var copied by rememberSaveable { mutableStateOf(false) }
    TextButton(
        onClick = {
            scope.launch { clipboard.setClipEntry(ClipEntry(ClipData.newPlainText("", text))) }
            copied = true
        },
        modifier = Modifier.heightIn(min = TouchTarget)
    ) { Text(if (copied) stringResource(UiR.string.security_codes_copied) else label) }
}

@Composable
private fun Password(label: String, value: String, hint: String? = null, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        singleLine = true,
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
        supportingText = hint?.let { { Text(it) } },
        modifier = Modifier.fillMaxWidth()
    )
}

@Composable
private fun Code(value: String, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = { onChange(it.filter(Char::isDigit).take(6)) },
        label = { Text(stringResource(UiR.string.auth_totp_code)) },
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
        modifier = Modifier.fillMaxWidth()
    )
}
