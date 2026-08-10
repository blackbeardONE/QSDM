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
	var registerToggle = document.getElementById('registerToggle');
	var confirmPasswordGroup = document.getElementById('confirmPasswordGroup');
	var passwordPolicy = document.getElementById('passwordPolicy');
	var modeHelp = document.getElementById('modeHelp');
	var registering = false;

	if (registerToggle) {
		registerToggle.addEventListener('click', function () {
			registering = !registering;
			if (confirmPasswordGroup) confirmPasswordGroup.hidden = !registering;
			if (passwordPolicy) passwordPolicy.hidden = !registering;
			registerToggle.textContent = registering ? 'Back to login' : 'Create dashboard login';
			if (modeHelp) {
				modeHelp.textContent = registering
					? 'Create a persistent dashboard account on this validator.'
					: 'Sign in with a dashboard account registered on this validator. This password is separate from your QSDM wallet passphrase.';
			}
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
		var address = String(formData.get('address') || '').trim();
		var password = String(formData.get('password') || '');
		try {
			if (registering) {
				var confirmation = String(formData.get('confirmPassword') || '');
				var registrationPasswordError = '';
				if (password !== confirmation) {
					registrationPasswordError = 'passwords do not match';
				} else if (
					password.length < 12 ||
					password.length > 256 ||
					!/[A-Z]/.test(password) ||
					!/[a-z]/.test(password) ||
					!/[0-9]/.test(password) ||
					!/[^A-Za-z0-9]/.test(password)
				) {
					registrationPasswordError =
						'Password must be at least 12 characters and include uppercase, lowercase, number, and symbol.';
				}
				if (registrationPasswordError) {
					errEl.textContent = registrationPasswordError;
					return;
				}

				if (stEl) stEl.textContent = 'Creating dashboard login…';
				var registration = await fetch('/api/v1/auth/register', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
					credentials: 'include',
					body: JSON.stringify({ address: address, password: password })
				});
				var rawRegistration = await registration.text();
				if (!registration.ok) {
					errEl.textContent =
						(errFromBody(registration, rawRegistration) || 'Registration failed') +
						retryAfterMessage(registration);
					if (stEl) stEl.textContent = '';
					return;
				}
				registering = false;
			}

			if (stEl) stEl.textContent = 'Signing in…';
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
				errEl.textContent =
					(errFromBody(response, rawLogin) || 'Login failed') +
					retryAfterMessage(response);
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
			if (btn) btn.disabled = false;
		}
	});
})();
