// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.onboarding

import android.content.Intent
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import org.simpleapb.api.model.SignupRequest
import org.simpleapb.app.R
import org.simpleapb.app.ui.AppAlertDialog
import org.simpleapb.app.ui.BusyButton
import org.simpleapb.app.ui.FormColumn
import org.simpleapb.app.ui.Notice
import org.simpleapb.app.ui.TouchTarget
import org.simpleapb.app.ui.errorText
import org.simpleapb.core.data.net.ServerUrl
import org.simpleapb.core.ui.R as UiR

@Composable
private fun Heading(text: String) {
    Text(text, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() })
}

@Composable
fun WelcomeScreen(onConnect: () -> Unit, onLocal: () -> Unit) {
    FormColumn {
        Image(
            painter = painterResource(R.drawable.ic_launcher_foreground),
            contentDescription = null,
            modifier = Modifier.size(120.dp).align(Alignment.CenterHorizontally)
        )
        Heading(stringResource(UiR.string.app_welcome_title))
        Text(stringResource(UiR.string.app_welcome_body), style = MaterialTheme.typography.bodyLarge)
        BusyButton(stringResource(UiR.string.app_welcome_connect), busy = false, onClick = onConnect)
        // The other path (brief §7.4): play audiobook files already on this device, no server.
        OutlinedButton(onClick = onLocal, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_welcome_local))
        }
    }
}

/** A preset build's first screen: connecting to the built-in server, with a retry when it can't. */
@Composable
fun PresetConnectScreen(vm: OnboardingViewModel, server: String, onReady: () -> Unit) {
    val s by vm.state.collectAsStateWithLifecycle()
    LaunchedEffect(server) { vm.connectPreset(server, onReady) }
    FormColumn {
        Heading(stringResource(UiR.string.app_welcome_title))
        val info = s.server
        when {
            s.busy || (info == null && s.error == null) -> Text(stringResource(UiR.string.app_preset_connecting))
            info?.updateRequired == true -> Notice(stringResource(UiR.string.app_server_update_required))
            info?.instance?.setupRequired == true -> Notice(stringResource(UiR.string.app_server_setup_required))
            s.error != null -> {
                Notice(stringResource(UiR.string.app_preset_unreachable))
                BusyButton(stringResource(UiR.string.app_preset_retry), busy = false, onClick = { vm.connectPreset(server, onReady) })
            }
        }
    }
}

@Composable
fun ServerScreen(vm: OnboardingViewModel, onReady: () -> Unit) {
    val s by vm.state.collectAsStateWithLifecycle()
    var scanning by rememberSaveable { mutableStateOf(false) }
    FormColumn {
        Heading(stringResource(UiR.string.app_server_title))
        if (scanning) {
            QrScanner(
                onResult = { text ->
                    scanning = false
                    vm.setAddress(text.trim())
                    vm.connect(onReady)
                },
                onCancel = { scanning = false }
            )
            return@FormColumn
        }
        OutlinedTextField(
            value = s.address,
            onValueChange = vm::setAddress,
            label = { Text(stringResource(UiR.string.app_server_address)) },
            supportingText = { Text(stringResource(UiR.string.app_server_address_hint)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
            keyboardActions = KeyboardActions(onGo = { vm.connect(onReady) }),
            modifier = Modifier.fillMaxWidth()
        )
        s.error?.let { Notice(errorText(it)) }
        val server = s.server
        when {
            server == null -> {}
            server.updateRequired -> Notice(stringResource(UiR.string.app_server_update_required))
            server.instance.setupRequired -> Notice(stringResource(UiR.string.app_server_setup_required))
            ServerUrl.isInsecure(server.url) && !s.insecureAccepted -> {
                Notice(stringResource(UiR.string.app_server_insecure))
                OutlinedButton(
                    onClick = { vm.acceptInsecure(onReady) },
                    modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.app_server_insecure_continue)) }
            }
        }
        BusyButton(
            stringResource(UiR.string.common_continue),
            busy = s.busy,
            onClick = { vm.connect(onReady) },
            enabled = s.address.isNotBlank()
        )
        OutlinedButton(onClick = { scanning = true }, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_server_scan))
        }
    }
}

@Composable
fun SignInScreen(vm: OnboardingViewModel, onSignedIn: () -> Unit, onRequestAccount: () -> Unit) {
    val s by vm.state.collectAsStateWithLifecycle()
    val server = s.server ?: return
    val context = LocalContext.current
    var username by rememberSaveable { mutableStateOf("") }
    var password by rememberSaveable { mutableStateOf("") }
    var code by rememberSaveable { mutableStateOf("") }
    FormColumn {
        Heading(stringResource(UiR.string.app_signin_title, server.instance.name))
        if (server.updateRecommended) Notice(stringResource(UiR.string.app_server_update_recommended), error = false)
        if (s.challenge == null) {
            OutlinedTextField(
                value = username,
                onValueChange = { username = it },
                label = { Text(stringResource(UiR.string.auth_username)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth()
            )
            OutlinedTextField(
                value = password,
                onValueChange = { password = it },
                label = { Text(stringResource(UiR.string.auth_password)) },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { vm.signIn(username, password, onSignedIn) }),
                modifier = Modifier.fillMaxWidth()
            )
            s.error?.let { Notice(errorText(it)) }
            BusyButton(
                stringResource(UiR.string.auth_sign_in),
                busy = s.busy,
                onClick = { vm.signIn(username, password, onSignedIn) },
                enabled = username.isNotBlank() && password.isNotEmpty()
            )
            if (server.instance.emailPasswordReset == true) {
                TextButton(
                    onClick = { context.startActivity(Intent(Intent.ACTION_VIEW, "${server.url}/forgot-password".toUri())) },
                    modifier = Modifier.heightIn(min = TouchTarget)
                ) { Text(stringResource(UiR.string.forgot_link)) }
            }
            if (server.instance.signupOpen) {
                Text(stringResource(UiR.string.auth_no_account))
                OutlinedButton(onClick = onRequestAccount, modifier = Modifier.fillMaxWidth().heightIn(min = TouchTarget)) {
                    Text(stringResource(UiR.string.auth_request_account))
                }
            }
        } else {
            Text(stringResource(if (s.useRecovery) UiR.string.auth_recovery_help else UiR.string.auth_totp_help))
            OutlinedTextField(
                value = code,
                onValueChange = { code = it },
                label = { Text(stringResource(if (s.useRecovery) UiR.string.auth_recovery_code else UiR.string.auth_totp_code)) },
                singleLine = true,
                keyboardOptions =
                KeyboardOptions(
                    keyboardType = if (s.useRecovery) KeyboardType.Ascii else KeyboardType.NumberPassword,
                    imeAction = ImeAction.Done,
                    autoCorrectEnabled = false
                ),
                keyboardActions = KeyboardActions(onDone = { vm.verify(code, onSignedIn) }),
                modifier = Modifier.fillMaxWidth()
            )
            s.error?.let { Notice(errorText(it)) }
            BusyButton(
                stringResource(UiR.string.auth_verify),
                busy = s.busy,
                onClick = { vm.verify(code, onSignedIn) },
                enabled = code.isNotBlank()
            )
            TextButton(onClick = {
                code = ""
                vm.toggleRecovery()
            }, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(if (s.useRecovery) UiR.string.auth_use_app_code else UiR.string.auth_use_recovery))
            }
            TextButton(onClick = vm::backToPassword, modifier = Modifier.heightIn(min = TouchTarget)) {
                Text(stringResource(UiR.string.common_back))
            }
        }
    }
}

@Composable
private fun CheckRow(checked: Boolean, onChange: (Boolean) -> Unit, label: String) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        modifier =
        Modifier
            .fillMaxWidth()
            .heightIn(min = TouchTarget)
            .toggleable(value = checked, role = Role.Checkbox, onValueChange = onChange)
    ) {
        Checkbox(checked = checked, onCheckedChange = null)
        Text(label)
    }
}

@Composable
fun RequestAccountScreen(vm: OnboardingViewModel, onBackToSignIn: () -> Unit) {
    val s by vm.state.collectAsStateWithLifecycle()
    val server = s.server ?: return
    var username by rememberSaveable { mutableStateOf("") }
    var password by rememberSaveable { mutableStateOf("") }
    var repeat by rememberSaveable { mutableStateOf("") }
    var email by rememberSaveable { mutableStateOf("") }
    var message by rememberSaveable { mutableStateOf("") }
    var age by rememberSaveable { mutableStateOf(false) }
    var terms by rememberSaveable { mutableStateOf(false) }
    var showTerms by rememberSaveable { mutableStateOf(false) }
    val mismatch = repeat.isNotEmpty() && password != repeat
    FormColumn {
        Heading(stringResource(UiR.string.auth_request_account))
        if (s.signupSent) {
            Notice(stringResource(UiR.string.auth_signup_sent), error = false)
            BusyButton(stringResource(UiR.string.auth_sign_in), busy = false, onClick = onBackToSignIn)
            return@FormColumn
        }
        OutlinedTextField(
            value = username,
            onValueChange = { username = it },
            label = { Text(stringResource(UiR.string.auth_username)) },
            supportingText = { Text(stringResource(UiR.string.auth_username_hint)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth()
        )
        OutlinedTextField(
            value = password,
            onValueChange = { password = it },
            label = { Text(stringResource(UiR.string.auth_password)) },
            supportingText = { Text(stringResource(UiR.string.auth_password_hint)) },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth()
        )
        OutlinedTextField(
            value = repeat,
            onValueChange = { repeat = it },
            label = { Text(stringResource(UiR.string.auth_password_repeat)) },
            isError = mismatch,
            supportingText = if (mismatch) ({ Text(stringResource(UiR.string.security_password_mismatch)) }) else null,
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth()
        )
        OutlinedTextField(
            value = email,
            onValueChange = { email = it },
            label = { Text(stringResource(UiR.string.auth_email_optional)) },
            supportingText = { Text(stringResource(UiR.string.auth_email_hint)) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
            modifier = Modifier.fillMaxWidth()
        )
        OutlinedTextField(
            value = message,
            onValueChange = { message = it },
            label = { Text(stringResource(UiR.string.auth_message)) },
            minLines = 2,
            modifier = Modifier.fillMaxWidth()
        )
        CheckRow(age, { age = it }, stringResource(UiR.string.auth_age_confirm))
        CheckRow(terms, { terms = it }, stringResource(UiR.string.auth_terms_accept))
        TextButton(onClick = { showTerms = true }, modifier = Modifier.heightIn(min = TouchTarget)) {
            Text(stringResource(UiR.string.app_signup_terms_link))
        }
        s.error?.let { Notice(errorText(it)) }
        BusyButton(
            stringResource(UiR.string.auth_send_request),
            busy = s.busy,
            enabled = username.isNotBlank() && password.isNotEmpty() && password == repeat && age && terms,
            onClick = {
                vm.requestAccount(
                    SignupRequest(
                        username = username.trim(),
                        password = password,
                        ageConfirmed = age,
                        termsAccepted = terms,
                        email = email.trim().ifEmpty { null },
                        message = message.trim().ifEmpty { null }
                    )
                )
            }
        )
    }
    if (showTerms) {
        val text =
            listOfNotNull(server.instance.privacyText, server.instance.termsText)
                .filter { it.isNotBlank() }
                .joinToString("\n\n")
                .ifEmpty { stringResource(UiR.string.app_signup_none) }
        AppAlertDialog(
            onDismissRequest = { showTerms = false },
            title = { Text(stringResource(UiR.string.app_signup_terms_title)) },
            text = { Text(text) },
            confirmButton = { TextButton(onClick = { showTerms = false }) { Text(stringResource(UiR.string.common_close)) } }
        )
    }
}
