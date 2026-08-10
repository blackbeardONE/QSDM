(function () {
	var reasons = {
		no_session: 'You need to sign in to open the dashboard.',
		bad_token: 'Your session is invalid or expired. Sign in again.',
		forbidden: 'This account role cannot use the dashboard.',
		credentials_in_url:
			'Your address or password was in the page URL (the browser submitted the form with GET). That cannot sign you in and is unsafe. The address bar has been cleared — enter address and password only in the boxes below and click Login. If this page still shows an old blue "Note: use API…" box, rebuild/restart qsdm (Docker or binary) so you get the current login page and scripts.'
	};

	function showFromQuery() {
		var errEl = document.getElementById('error');
		if (!errEl) return;
		try {
			var params = new URLSearchParams(window.location.search);
			var r = params.get('reason');
			if (r && reasons[r]) {
				errEl.textContent = reasons[r];
			}
		} catch (e) {}
	}

	// After a successful login we set this flag and go to /. If we land here again, the dashboard rejected the session (often: old binary, wrong host, or API not wired).
	function showIfBouncedFromLogin() {
		var errEl = document.getElementById('error');
		if (!errEl || errEl.textContent) return;
		try {
			if (sessionStorage.getItem('qsdm_expect_dashboard') === '1') {
				sessionStorage.removeItem('qsdm_expect_dashboard');
				errEl.textContent =
					'You were sent back to this page: the dashboard did not accept your session. Rebuild and restart the node (so /api/v1/auth/login is proxied, not redirected), use the same hostname as when you signed in (127.0.0.1 vs localhost), and register again after a restart.';
			}
		} catch (e) {}
	}

	function parseJsonSafe(raw) {
		try {
			return raw ? JSON.parse(raw) : {};
		} catch (e) {
			return null;
		}
	}

	function errFromBody(response, raw) {
		var ct = (response.headers.get('content-type') || '').toLowerCase();
		if (ct.indexOf('application/json') >= 0) {
			var j = parseJsonSafe(raw);
			if (j && (j.message || j.error)) {
				return j.message || j.error;
			}
		}
		if (raw && raw.length) {
			return raw.length > 280 ? raw.slice(0, 280) + '…' : raw;
		}
		return 'HTTP ' + response.status;
	}

	function retryAfterMessage(response) {
		var retryAfter = response.headers.get('Retry-After');
		return retryAfter ? ' Try again in ' + retryAfter + ' seconds.' : '';
	}

	showFromQuery();
	showIfBouncedFromLogin();

	var form = document.getElementById('loginForm');
	if (!form) {
		return;
	}
	var mode = 'login';
	var toggle = document.getElementById('registerToggle');
	var confirmGroup = document.getElementById('confirmPasswordGroup');
	var confirmInput = form.elements.confirmPassword;
	var passwordInput = form.elements.password;
	var modeHelp = document.getElementById('modeHelp');
	var passwordPolicy = document.getElementById('passwordPolicy');
	var submitButton = document.getElementById('loginSubmit');
	var blockedUntil = 0;
	var retryTimer = null;

	function registrationPasswordError(password) {
		if (password.length < 12) return 'Dashboard passwords must be at least 12 characters.';
		if (password.length > 256) return 'Dashboard passwords must be no more than 256 characters.';
		var missing = [];
		if (!/[A-Z]/.test(password)) missing.push('uppercase letter');
		if (!/[a-z]/.test(password)) missing.push('lowercase letter');
		if (!/[0-9]/.test(password)) missing.push('number');
		if (!/[!-/:-@[-`{-~]/.test(password)) missing.push('symbol');
		return missing.length ? 'Password needs at least one ' + missing.join(', ') + '.' : '';
	}

	function beginRetryWait(response, errEl, stEl) {
		var seconds = parseInt(response.headers.get('Retry-After') || '60', 10);
		if (!isFinite(seconds) || seconds < 1) seconds = 60;
		blockedUntil = Date.now() + seconds * 1000;
		if (retryTimer) window.clearInterval(retryTimer);
		function update() {
			var remaining = Math.max(0, Math.ceil((blockedUntil - Date.now()) / 1000));
			if (remaining > 0) {
				errEl.textContent = 'Too many attempts. Try again in ' + remaining + ' seconds.';
				if (submitButton) submitButton.disabled = true;
				if (toggle) toggle.disabled = true;
				if (stEl) stEl.textContent = '';
				return;
			}
			window.clearInterval(retryTimer);
			retryTimer = null;
			blockedUntil = 0;
			if (submitButton) submitButton.disabled = false;
			if (toggle) toggle.disabled = false;
			errEl.textContent = 'You can try again now.';
		}
		update();
		retryTimer = window.setInterval(update, 1000);
	}

	function setMode(nextMode) {
		mode = nextMode === 'register' ? 'register' : 'login';
		var registering = mode === 'register';
		if (confirmGroup) confirmGroup.hidden = !registering;
		if (confirmInput) {
			confirmInput.required = registering;
			confirmInput.minLength = registering ? 12 : 0;
			confirmInput.maxLength = 256;
			if (!registering) confirmInput.value = '';
		}
		if (passwordInput) {
			passwordInput.autocomplete = registering ? 'new-password' : 'current-password';
			passwordInput.minLength = registering ? 12 : 0;
			passwordInput.maxLength = 256;
		}
		if (passwordPolicy) passwordPolicy.hidden = !registering;
		if (submitButton) submitButton.textContent = registering ? 'Create and sign in' : 'Login';
		if (toggle) toggle.textContent = registering ? 'Back to login' : 'Create dashboard login';
		if (modeHelp) {
			modeHelp.textContent = registering
				? 'Register this QSDM wallet address for this validator dashboard. Choose a dedicated dashboard password; do not enter or reuse your wallet keystore passphrase.'
				: 'Sign in with a dashboard account registered on this validator. This password is separate from your QSDM wallet passphrase.';
		}
		var errEl = document.getElementById('error');
		var stEl = document.getElementById('status');
		if (errEl) errEl.textContent = '';
		if (stEl) stEl.textContent = '';
	}

	if (toggle) {
		toggle.addEventListener('click', function () {
			setMode(mode === 'login' ? 'register' : 'login');
		});
	}

	form.addEventListener('submit', async function (e) {
		e.preventDefault();
		var errEl = document.getElementById('error');
		var stEl = document.getElementById('status');
		var btn = document.getElementById('loginSubmit');
		errEl.textContent = '';
		if (stEl) stEl.textContent = '';
		if (btn) btn.disabled = true;

		var formData = new FormData(form);
		var address = String(formData.get('address') || '').trim().toLowerCase();
		var password = String(formData.get('password') || '');
		if (Date.now() < blockedUntil) {
			if (btn) btn.disabled = true;
			return;
		}
		if (mode === 'register') {
			var passwordError = registrationPasswordError(password);
			if (passwordError) {
				errEl.textContent = passwordError;
				if (btn) btn.disabled = false;
				return;
			}
		}
		if (mode === 'register' && password !== String(formData.get('confirmPassword') || '')) {
			errEl.textContent = 'The passwords do not match.';
			if (btn) btn.disabled = false;
			return;
		}
		try {
			if (mode === 'register') {
				if (stEl) stEl.textContent = 'Creating dashboard login…';
				var registerResponse = await fetch('/api/v1/auth/register', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
					credentials: 'same-origin',
					body: JSON.stringify({ address: address, password: password })
				});
				var rawRegister = await registerResponse.text();
				if (!registerResponse.ok) {
					if (registerResponse.status === 429) {
						beginRetryWait(registerResponse, errEl, stEl);
						return;
					}
					errEl.textContent =
						(errFromBody(registerResponse, rawRegister) || 'Registration failed') +
						retryAfterMessage(registerResponse);
					if (stEl) stEl.textContent = '';
					return;
				}
				if (stEl) stEl.textContent = 'Account created. Signing in…';
			} else if (stEl) {
				stEl.textContent = 'Signing in…';
			}
			// Keep the API access token server-side. The dashboard endpoint
			// authenticates against the API, stores the token in its session
			// table, and returns only the small HttpOnly session cookie.
			var response = await fetch('/api/auth/login', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
				credentials: 'include',
				body: JSON.stringify({
					address: address,
					password: password
				})
			});

			var rawLogin = await response.text();
			var data = parseJsonSafe(rawLogin);
			if (data === null) {
				errEl.textContent = 'Login: expected JSON, got (HTTP ' + response.status + '): ' + (rawLogin ? rawLogin.slice(0, 200) : '(empty)');
				if (stEl) stEl.textContent = '';
				return;
			}

			if (!response.ok) {
				if (response.status === 429) {
					beginRetryWait(response, errEl, stEl);
					return;
				}
				var loginError = errFromBody(response, rawLogin) || 'Login failed';
				if (response.status === 401 && mode === 'login') {
					loginError += ' If this is your first dashboard login on this validator, choose Create dashboard login.';
				}
				errEl.textContent = loginError + retryAfterMessage(response);
				if (stEl) stEl.textContent = '';
				return;
			}

			if (stEl) stEl.textContent = 'Redirecting…';
			try {
				sessionStorage.setItem('qsdm_expect_dashboard', '1');
			} catch (e2) {}
			window.location.href = '/';
		} catch (err) {
			errEl.textContent = err && err.message ? err.message : String(err);
			if (stEl) stEl.textContent = '';
		} finally {
			if (btn && Date.now() >= blockedUntil) btn.disabled = false;
		}
	});
})();
