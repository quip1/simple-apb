// SPDX-License-Identifier: AGPL-3.0-or-later
package org.simpleapb.app.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.simpleapb.api.model.SignupRequest
import org.simpleapb.core.data.auth.AuthRepository
import org.simpleapb.core.data.auth.Outcome
import org.simpleapb.core.data.auth.ServerInfo
import org.simpleapb.core.data.auth.SignInResult
import org.simpleapb.core.data.net.ApiFailure
import org.simpleapb.core.data.net.ServerUrl

data class OnboardingState(
    val address: String = "",
    val server: ServerInfo? = null,
    /** The user accepted signing in over plain http. */
    val insecureAccepted: Boolean = false,
    val busy: Boolean = false,
    val error: ApiFailure? = null,
    /** Set after the password step when a 2FA code is needed. */
    val challenge: String? = null,
    val useRecovery: Boolean = false,
    val signupSent: Boolean = false
)

/** Connect → sign in (with a 2FA code) or request an account (brief §7.4). */
@HiltViewModel
class OnboardingViewModel
@Inject
constructor(private val auth: AuthRepository) : ViewModel() {
    private val _state = MutableStateFlow(OnboardingState())
    val state: StateFlow<OnboardingState> = _state.asStateFlow()

    fun setAddress(value: String) = _state.update { it.copy(address = value, error = null) }

    /** Checks the address; calls [onReady] when the sign-in screen can open. */
    fun connect(onReady: () -> Unit) {
        val s = _state.value
        if (s.busy) return
        _state.update { it.copy(busy = true, error = null, server = null, insecureAccepted = false) }
        viewModelScope.launch {
            when (val r = auth.probe(s.address)) {
                is Outcome.Failed -> _state.update { it.copy(busy = false, error = r.failure) }
                is Outcome.Ok -> {
                    _state.update { it.copy(busy = false, server = r.value, challenge = null, signupSent = false) }
                    if (canProceed(r.value, accepted = false)) onReady()
                }
            }
        }
    }

    /** After the http warning: the user chose to continue. */
    fun acceptInsecure(onReady: () -> Unit) {
        _state.update { it.copy(insecureAccepted = true) }
        val server = _state.value.server ?: return
        if (canProceed(server, accepted = true)) onReady()
    }

    private fun canProceed(server: ServerInfo, accepted: Boolean) =
        !server.updateRequired && !server.instance.setupRequired && (accepted || !ServerUrl.isInsecure(server.url))

    fun signIn(username: String, password: String, onSignedIn: () -> Unit) {
        val server = _state.value.server ?: return
        run({ auth.signIn(server, username, password) }, onSignedIn)
    }

    fun verify(code: String, onSignedIn: () -> Unit) {
        val s = _state.value
        val server = s.server ?: return
        val challenge = s.challenge ?: return
        run({ auth.verifyCode(server, challenge, code, s.useRecovery) }, onSignedIn)
    }

    fun toggleRecovery() = _state.update { it.copy(useRecovery = !it.useRecovery, error = null) }

    fun backToPassword() = _state.update { it.copy(challenge = null, error = null, useRecovery = false) }

    private fun run(block: suspend () -> SignInResult, onSignedIn: () -> Unit) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (val r = block()) {
                is SignInResult.Failed -> _state.update { it.copy(busy = false, error = r.failure) }
                is SignInResult.NeedsCode -> _state.update { it.copy(busy = false, challenge = r.challenge) }
                is SignInResult.SignedIn -> {
                    // An account that must enrol 2FA does that first, in the app (EnrollRequiredScreen).
                    _state.update { it.copy(busy = false) }
                    onSignedIn()
                }
            }
        }
    }

    fun requestAccount(request: SignupRequest) {
        val server = _state.value.server ?: return
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (val r = auth.requestAccount(server, request)) {
                is Outcome.Failed -> _state.update { it.copy(busy = false, error = r.failure) }
                is Outcome.Ok -> _state.update { it.copy(busy = false, signupSent = true) }
            }
        }
    }

    fun clearError() = _state.update { it.copy(error = null) }
}
