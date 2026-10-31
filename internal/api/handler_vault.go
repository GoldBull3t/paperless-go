package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/vault"
)

type VaultHandler struct {
	vm       *vault.VaultManager
	mediaDir string
}

func NewVaultHandler(vm *vault.VaultManager, mediaDir ...string) *VaultHandler {
	vh := &VaultHandler{vm: vm}
	if len(mediaDir) > 0 {
		vh.mediaDir = mediaDir[0]
	}
	return vh
}

// Setup (/api/vault/setup/) initializes master password and returns emergency recovery key
func (h *VaultHandler) Setup(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		http.Error(w, `{"detail": "Only superusers can configure the secure vault"}`, http.StatusForbidden)
		return
	}

	var input struct {
		MasterPassword  string `json:"master_password"`
		AutoLockMinutes int    `json:"auto_lock_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	recoveryKey, err := h.vm.Setup(input.MasterPassword, input.AutoLockMinutes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"result":       "OK",
		"message":      "Vault successfully configured and unlocked",
		"recovery_key": recoveryKey,
	})
}

// Unlock (/api/vault/unlock/) unlocks the vault for user session
func (h *VaultHandler) Unlock(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MasterPassword string `json:"master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ok, remainingSec, err := h.vm.Unlock(input.MasterPassword)
	if !ok || err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "Invalid master password"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"unlocked":           true,
		"expires_in_seconds": remainingSec,
	})
}

// Lock (/api/vault/lock/) locks the vault immediately and zeroes out memory keys
func (h *VaultHandler) Lock(w http.ResponseWriter, r *http.Request) {
	h.vm.Lock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"unlocked": false, "message": "Vault locked"}`))
}

// Status (/api/vault/status/) reports vault configuration and lock state
func (h *VaultHandler) Status(w http.ResponseWriter, r *http.Request) {
	enabled, unlocked, remSec, docCount, err := h.vm.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":               enabled,
		"unlocked":              unlocked,
		"expires_in_seconds":    remSec,
		"vault_documents_count": docCount,
	})
}

// ChangePassword (/api/vault/change_password/) rotates master password and re-wraps VMEK
func (h *VaultHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		http.Error(w, `{"detail": "Only superusers can change vault master password"}`, http.StatusForbidden)
		return
	}

	var input struct {
		OldMasterPassword string `json:"old_master_password"`
		NewMasterPassword string `json:"new_master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.vm.ChangeMasterPassword(input.OldMasterPassword, input.NewMasterPassword); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"result":  "OK",
		"message": "Master password changed and keys re-wrapped successfully",
	})
}

// Recover (/api/vault/recover/) recovers vault access with Emergency Recovery Key
func (h *VaultHandler) Recover(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RecoveryKey       string `json:"recovery_key"`
		NewMasterPassword string `json:"new_master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.vm.Recover(input.RecoveryKey, input.NewMasterPassword); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"result":  "OK",
		"message": "Vault successfully recovered and unlocked with new master password",
	})
}

// RotateKey (/api/vault/rotate_key/) rotates VMEK and re-encrypts all vault documents on physical disk
func (h *VaultHandler) RotateKey(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		http.Error(w, `{"detail": "Only superusers can rotate the vault encryption key"}`, http.StatusForbidden)
		return
	}

	var input struct {
		MasterPassword string `json:"master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	count, err := h.vm.RotateVaultKey(input.MasterPassword, h.mediaDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"result":                "OK",
		"reencrypted_documents": count,
		"message":               "Vault master encryption key rotated and documents re-encrypted",
	})
}

// ToggleDocumentVault (/api/documents/{id}/vault/) marks or unmarks document as vault protected
func (h *VaultHandler) ToggleDocumentVault(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if !h.vm.IsUnlocked() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "Vault is locked. Unlock vault to modify protected documents."}`))
		return
	}

	isVault, err := h.vm.ToggleDocument(id, h.mediaDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"document_id": id,
		"is_vault":    isVault,
		"message":     "Document vault status updated",
	})
}

// VaultPage serves the dedicated, modern web portal for secure vault management
func (h *VaultHandler) VaultPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	html := `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Cryptographic Vault - Paperless-ngx</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" href="/static/bootstrap.min.css">
	<style>
		:root {
			--bg-app: #f4f6f8;
			--bg-card: #ffffff;
			--text-main: #212529;
			--text-muted: #6c757d;
			--border-color: #dee2e6;
			--primary-color: #17541f;
			--primary-hover: #124018;
			--primary-focus-shadow: rgba(23, 84, 31, 0.25);
			--input-bg: #ffffff;
			--danger-color: #dc3545;
			--success-color: #198754;
			--warning-color: #ffc107;
		}
		@media (prefers-color-scheme: dark) {
			:root {
				--bg-app: #121315;
				--bg-card: #242529;
				--text-main: #e2e8f0;
				--text-muted: #94a3b8;
				--border-color: #34373d;
				--primary-color: #1f7a2d;
				--primary-hover: #279939;
				--primary-focus-shadow: rgba(31, 122, 45, 0.35);
				--input-bg: #1c1d21;
			}
		}
		body {
			background-color: var(--bg-app);
			color: var(--text-main);
			font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
			margin: 0;
			min-height: 100vh;
			display: flex;
			flex-direction: column;
		}
		.nav-header {
			background: var(--bg-card);
			border-bottom: 1px solid var(--border-color);
			padding: 0.85rem 1.5rem;
			display: flex;
			align-items: center;
			justify-content: space-between;
		}
		.card-vault {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 14px;
			box-shadow: 0 4px 20px rgba(0,0,0,0.06);
			padding: 2.25rem;
			margin-bottom: 2rem;
		}
		.form-control, .form-select {
			background-color: var(--input-bg);
			border-color: var(--border-color);
			color: var(--text-main);
			border-radius: 8px;
		}
		.form-control:focus, .form-select:focus {
			background-color: var(--input-bg);
			color: var(--text-main);
			border-color: var(--primary-color);
			box-shadow: 0 0 0 0.25rem var(--primary-focus-shadow);
		}
		.btn-primary {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
			font-weight: 600;
			border-radius: 8px;
		}
		.btn-primary:hover {
			background-color: var(--primary-hover);
			border-color: var(--primary-hover);
		}
		.status-badge {
			padding: 0.45rem 0.85rem;
			border-radius: 20px;
			font-size: 0.85rem;
			font-weight: 600;
			display: inline-flex;
			align-items: center;
			gap: 6px;
		}
		.recovery-box {
			background: rgba(0,0,0,0.04);
			border: 1px dashed var(--border-color);
			border-radius: 8px;
			padding: 1rem;
			font-family: monospace;
			font-size: 1.1rem;
			letter-spacing: 1.5px;
			word-break: break-all;
			user-select: all;
		}
		@media (prefers-color-scheme: dark) {
			.recovery-box {
				background: rgba(255,255,255,0.05);
			}
		}
		.toast-notice {
			position: fixed;
			bottom: 24px;
			right: 24px;
			background: var(--primary-color);
			color: #fff;
			padding: 12px 20px;
			border-radius: 8px;
			box-shadow: 0 6px 18px rgba(0,0,0,0.2);
			display: none;
			z-index: 1050;
			font-size: 0.95rem;
		}
	</style>
</head>
<body>
	<header class="nav-header">
		<div class="d-flex align-items-center gap-2">
			<svg width="28" height="28" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
				<rect width="48" height="48" rx="8" fill="var(--primary-color)"/>
				<path d="M14 12C14 10.8954 14.8954 10 16 10H26L34 18V36C34 37.1046 33.1046 38 32 38H16C14.8954 38 14 37.1046 14 36V12Z" fill="white" fill-opacity="0.95"/>
				<path d="M26 10V18H34L26 10Z" fill="white" fill-opacity="0.6"/>
			</svg>
			<span class="fw-bold fs-5">Paperless-ngx</span>
			<span class="badge bg-secondary ms-1">Security Vault</span>
		</div>
		<a href="/" class="btn btn-outline-secondary btn-sm">
			&larr; Back to Dashboard
		</a>
	</header>

	<main class="container py-5" style="max-width: 680px;">
		<!-- Status Hero -->
		<div class="card-vault text-center" id="heroStatusCard">
			<div id="statusIcon" class="mb-3">
				<svg width="52" height="52" fill="currentColor" class="text-secondary" viewBox="0 0 16 16">
					<path d="M8 1a2 2 0 0 1 2 2v4H6V3a2 2 0 0 1 2-2zm3 6V3a3 3 0 0 0-6 0v4a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2zM5 8h6a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z"/>
				</svg>
			</div>
			<h2 class="h4 mb-1" id="statusTitle">Checking Vault Status...</h2>
			<p class="text-muted small mb-3" id="statusDescription">Envelope Encryption with AES-256-GCM &amp; Argon2id</p>
			<div>
				<span class="status-badge bg-secondary text-white" id="statusBadge">Loading...</span>
			</div>
		</div>

		<!-- Container: Setup Vault (Shown if vault unconfigured) -->
		<div class="card-vault" id="setupCard" style="display:none;">
			<h3 class="h5 mb-2">Configure Cryptographic Vault</h3>
			<p class="text-muted small mb-4">Set a master passphrase to enable authenticated envelope encryption at rest for sensitive documents.</p>

			<form id="setupForm">
				<div class="mb-3">
					<label class="form-label fw-semibold" for="setupPass">Master Password</label>
					<input type="password" class="form-control" id="setupPass" required minlength="8" placeholder="At least 8 characters">
				</div>
				<div class="mb-3">
					<label class="form-label fw-semibold" for="setupPassConfirm">Confirm Master Password</label>
					<input type="password" class="form-control" id="setupPassConfirm" required minlength="8" placeholder="Re-enter password">
				</div>
				<div class="mb-4">
					<label class="form-label fw-semibold" for="autoLockSelect">Automatic Lock Timeout</label>
					<select class="form-select" id="autoLockSelect">
						<option value="15">15 Minutes</option>
						<option value="30" selected>30 Minutes (Recommended)</option>
						<option value="60">1 Hour</option>
						<option value="240">4 Hours</option>
						<option value="0">Disabled (Lock manually only)</option>
					</select>
				</div>
				<button type="submit" class="btn btn-primary w-100 py-2" id="setupSubmitBtn">
					Initialize &amp; Generate Recovery Key
				</button>
			</form>
		</div>

		<!-- Container: Unlock Vault (Shown if vault is locked) -->
		<div class="card-vault" id="unlockCard" style="display:none;">
			<h3 class="h5 mb-2">Unlock Cryptographic Vault</h3>
			<p class="text-muted small mb-4">Enter your master password to decrypt the Master Encryption Key in memory.</p>

			<form id="unlockForm">
				<div class="mb-3">
					<label class="form-label fw-semibold" for="unlockPass">Master Password</label>
					<input type="password" class="form-control" id="unlockPass" required autofocus placeholder="Enter master password">
				</div>
				<button type="submit" class="btn btn-primary w-100 py-2 mb-3" id="unlockSubmitBtn">
					Unlock Vault
				</button>
			</form>

			<div class="text-center pt-2">
				<button type="button" class="btn btn-link text-decoration-none small text-muted" id="btnShowRecovery">
					Forgot Master Password? Use Emergency Recovery Key &rarr;
				</button>
			</div>
		</div>

		<!-- Container: Unlocked Management (Shown if vault is unlocked) -->
		<div class="card-vault" id="unlockedCard" style="display:none;">
			<div class="d-flex align-items-center justify-content-between mb-3">
				<div>
					<h3 class="h5 mb-1">Vault Active &amp; Unlocked</h3>
					<div class="small text-muted" id="unlockedDocCount">0 protected documents</div>
				</div>
				<button type="button" class="btn btn-outline-danger btn-sm" id="lockNowBtn">
					<svg width="14" height="14" fill="currentColor" class="me-1" viewBox="0 0 16 16"><path d="M8 1a2 2 0 0 1 2 2v4H6V3a2 2 0 0 1 2-2zm3 6V3a3 3 0 0 0-6 0v4a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2z"/></svg>
					Lock Immediately
				</button>
			</div>

			<div class="alert alert-info py-2 small mb-4 d-flex align-items-center justify-content-between" id="autoLockTimerBox">
				<span>Auto-lock in: <strong id="countdownText">--:--</strong></span>
				<span class="text-muted">AES-256-GCM Hardware Accelerated</span>
			</div>

			<hr class="my-4" style="border-color: var(--border-color);">

			<h4 class="h6 mb-3">Vault Security Controls</h4>
			<div class="d-grid gap-2">
				<button type="button" class="btn btn-outline-secondary text-start p-3" id="btnChangePass">
					<div class="fw-semibold">Change Master Password</div>
					<div class="small text-muted">Re-wraps Master Key with new Argon2id salt without touching physical files</div>
				</button>
				<button type="button" class="btn btn-outline-secondary text-start p-3" id="btnRotateKey">
					<div class="fw-semibold">Rotate Encryption Key (VMEK)</div>
					<div class="small text-muted">Generates a fresh 256-bit VMEK and atomically re-encrypts all vault documents on disk</div>
				</button>
			</div>
		</div>

		<!-- Container: Emergency Recovery (Accordion/Panel) -->
		<div class="card-vault" id="recoveryCard" style="display:none;">
			<div class="d-flex align-items-center justify-content-between mb-3">
				<h3 class="h5 mb-0 text-danger">Emergency Vault Recovery</h3>
				<button type="button" class="btn btn-sm btn-outline-secondary" id="btnCancelRecovery">Cancel</button>
			</div>
			<p class="text-muted small mb-4">Paste your 32-character Emergency Recovery Key to decrypt the master key and reset your master password.</p>

			<form id="recoveryForm">
				<div class="mb-3">
					<label class="form-label fw-semibold" for="recKeyInput">Recovery Key</label>
					<input type="text" class="form-control font-monospace" id="recKeyInput" required placeholder="XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX" style="letter-spacing:1px;">
				</div>
				<div class="mb-4">
					<label class="form-label fw-semibold" for="newMasterPass">New Master Password</label>
					<input type="password" class="form-control" id="newMasterPass" required minlength="8" placeholder="Choose a new secure password">
				</div>
				<button type="submit" class="btn btn-danger w-100 py-2" id="recoverSubmitBtn">
					Recover Vault &amp; Reset Password
				</button>
			</form>
		</div>

		<!-- Recovery Key Created Modal (Alert) -->
		<div class="card-vault border-warning" id="recoveryKeyModalCard" style="display:none;">
			<div class="d-flex align-items-center gap-2 mb-2 text-warning">
				<svg width="24" height="24" fill="currentColor" viewBox="0 0 16 16"><path d="M8.982 1.566a1.13 1.13 0 0 0-1.96 0L.165 13.233c-.457.778.091 1.767.98 1.767h13.713c.889 0 1.438-.99.98-1.767L8.982 1.566zM8 5c.535 0 .954.462.9.995l-.35 3.507a.552.552 0 0 1-1.1 0L7.1 5.995A.905.905 0 0 1 8 5zm.002 6a1 1 0 1 1 0 2 1 1 0 0 1 0-2z"/></svg>
				<h4 class="h5 mb-0 fw-bold">Emergency Recovery Key Created!</h4>
			</div>
			<p class="small text-muted mb-3">Copy and store this key in a secure location (e.g. password manager or safe). If you lose your master password, this key is the <strong>only way</strong> to restore access to your encrypted documents.</p>

			<div class="recovery-box text-center fw-bold text-primary mb-3" id="displayRecoveryKey">XXXX-XXXX-XXXX</div>

			<div class="d-flex gap-2">
				<button type="button" class="btn btn-primary flex-grow-1" id="btnCopyRecKey">Copy Key to Clipboard</button>
				<button type="button" class="btn btn-outline-secondary" id="btnDismissRecKey">I Have Stored It Safely</button>
			</div>
		</div>
	</main>

	<div class="toast-notice" id="toastNotice">Notification</div>

	<script>
		let remainingSeconds = 0;
		let countdownTimer = null;

		function showToast(msg, isError) {
			const t = document.getElementById('toastNotice');
			t.textContent = msg;
			t.style.backgroundColor = isError ? 'var(--danger-color)' : 'var(--primary-color)';
			t.style.display = 'block';
			setTimeout(() => { t.style.display = 'none'; }, 3000);
		}

		function formatTime(sec) {
			if (sec <= 0) return '00:00';
			const m = Math.floor(sec / 60);
			const s = sec % 60;
			return (m < 10 ? '0' + m : m) + ':' + (s < 10 ? '0' + s : s);
		}

		function startCountdown(sec) {
			clearInterval(countdownTimer);
			remainingSeconds = sec;
			const el = document.getElementById('countdownText');
			if (sec <= 0) {
				document.getElementById('autoLockTimerBox').style.display = 'none';
				return;
			}
			document.getElementById('autoLockTimerBox').style.display = 'flex';
			el.textContent = formatTime(remainingSeconds);

			countdownTimer = setInterval(() => {
				remainingSeconds--;
				if (remainingSeconds <= 0) {
					clearInterval(countdownTimer);
					el.textContent = '00:00';
					refreshStatus();
				} else {
					el.textContent = formatTime(remainingSeconds);
				}
			}, 1000);
		}

		async function refreshStatus() {
			try {
				const res = await fetch('/api/vault/status/');
				if (!res.ok) throw new Error('Status failed');
				const st = await res.json();

				const heroBadge = document.getElementById('statusBadge');
				const heroTitle = document.getElementById('statusTitle');
				const heroIcon = document.getElementById('statusIcon');
				const setupCard = document.getElementById('setupCard');
				const unlockCard = document.getElementById('unlockCard');
				const unlockedCard = document.getElementById('unlockedCard');
				const recoveryCard = document.getElementById('recoveryCard');

				if (!st.enabled) {
					heroTitle.textContent = 'Vault Not Configured';
					heroBadge.className = 'status-badge bg-secondary text-white';
					heroBadge.textContent = 'Unconfigured';
					heroIcon.innerHTML = '<svg width="52" height="52" fill="currentColor" class="text-secondary" viewBox="0 0 16 16"><path d="M5.072.56C6.157.265 7.31 0 8 0s1.843.265 2.928.56c1.11.3 2.229.655 2.887.87a1.54 1.54 0 0 1 1.044 1.262c.596 4.477-.787 7.795-2.465 9.99a11.775 11.775 0 0 1-2.517 2.453 7.159 7.159 0 0 1-1.048.625c-.28.132-.581.24-.829.24s-.548-.108-.829-.24a7.158 7.158 0 0 1-1.048-.625 11.777 11.777 0 0 1-2.517-2.453C1.928 10.487.545 7.169 1.141 2.692A1.54 1.54 0 0 1 2.185 1.43 62.456 62.456 0 0 1 5.072.56z"/></svg>';
					setupCard.style.display = 'block';
					unlockCard.style.display = 'none';
					unlockedCard.style.display = 'none';
					recoveryCard.style.display = 'none';
					clearInterval(countdownTimer);
				} else if (!st.unlocked) {
					heroTitle.textContent = 'Vault Locked';
					heroBadge.className = 'status-badge bg-danger text-white';
					heroBadge.textContent = 'Locked';
					heroIcon.innerHTML = '<svg width="52" height="52" fill="currentColor" class="text-danger" viewBox="0 0 16 16"><path d="M8 1a2 2 0 0 1 2 2v4H6V3a2 2 0 0 1 2-2zm3 6V3a3 3 0 0 0-6 0v4a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2zM5 8h6a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z"/></svg>';
					setupCard.style.display = 'none';
					unlockCard.style.display = 'block';
					unlockedCard.style.display = 'none';
					recoveryCard.style.display = 'none';
					clearInterval(countdownTimer);
				} else {
					heroTitle.textContent = 'Vault Unlocked & Active';
					heroBadge.className = 'status-badge bg-success text-white';
					heroBadge.textContent = 'Unlocked';
					heroIcon.innerHTML = '<svg width="52" height="52" fill="currentColor" class="text-success" viewBox="0 0 16 16"><path d="M11 1a2 2 0 0 0-2 2v4a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2h5V3a3 3 0 0 1 6 0v4a.5.5 0 0 1-1 0V3a2 2 0 0 0-2-2zM3 8a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V9a1 1 0 0 0-1-1H3z"/></svg>';
					setupCard.style.display = 'none';
					unlockCard.style.display = 'none';
					unlockedCard.style.display = 'block';
					recoveryCard.style.display = 'none';
					document.getElementById('unlockedDocCount').textContent = (st.vault_documents_count || 0) + ' documents under encrypted protection';
					startCountdown(st.expires_in_seconds || 0);
				}
			} catch (e) {
				console.error(e);
			}
		}

		// Setup Form
		document.getElementById('setupForm').addEventListener('submit', async (e) => {
			e.preventDefault();
			const p1 = document.getElementById('setupPass').value;
			const p2 = document.getElementById('setupPassConfirm').value;
			if (p1 !== p2) {
				showToast('Passwords do not match', true);
				return;
			}
			const autoLock = parseInt(document.getElementById('autoLockSelect').value, 10);
			try {
				const res = await fetch('/api/vault/setup/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ master_password: p1, auto_lock_minutes: autoLock })
				});
				const data = await res.json();
				if (!res.ok) throw new Error(data.detail || data.error || 'Setup failed');

				document.getElementById('displayRecoveryKey').textContent = data.recovery_key;
				document.getElementById('recoveryKeyModalCard').style.display = 'block';
				document.getElementById('setupCard').style.display = 'none';
				showToast('Vault configured successfully!');
			} catch (err) {
				showToast(err.message, true);
			}
		});

		// Copy Recovery Key
		document.getElementById('btnCopyRecKey').addEventListener('click', () => {
			const key = document.getElementById('displayRecoveryKey').textContent;
			navigator.clipboard.writeText(key).then(() => {
				showToast('Recovery key copied to clipboard!');
			});
		});

		document.getElementById('btnDismissRecKey').addEventListener('click', () => {
			document.getElementById('recoveryKeyModalCard').style.display = 'none';
			refreshStatus();
		});

		// Unlock Form
		document.getElementById('unlockForm').addEventListener('submit', async (e) => {
			e.preventDefault();
			const pass = document.getElementById('unlockPass').value;
			try {
				const res = await fetch('/api/vault/unlock/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ master_password: pass })
				});
				const data = await res.json();
				if (!res.ok) throw new Error(data.detail || 'Invalid master password');
				document.getElementById('unlockPass').value = '';
				showToast('Vault unlocked!');
				refreshStatus();
			} catch (err) {
				showToast(err.message, true);
			}
		});

		// Lock Immediately
		document.getElementById('lockNowBtn').addEventListener('click', async () => {
			try {
				await fetch('/api/vault/lock/', { method: 'POST' });
				showToast('Vault locked.');
				refreshStatus();
			} catch (e) {
				console.error(e);
			}
		});

		// Show / Hide Recovery
		document.getElementById('btnShowRecovery').addEventListener('click', () => {
			document.getElementById('unlockCard').style.display = 'none';
			document.getElementById('recoveryCard').style.display = 'block';
		});

		document.getElementById('btnCancelRecovery').addEventListener('click', () => {
			document.getElementById('recoveryCard').style.display = 'none';
			document.getElementById('unlockCard').style.display = 'block';
		});

		// Recovery Form
		document.getElementById('recoveryForm').addEventListener('submit', async (e) => {
			e.preventDefault();
			const key = document.getElementById('recKeyInput').value.trim();
			const newPass = document.getElementById('newMasterPass').value;
			try {
				const res = await fetch('/api/vault/recover/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ recovery_key: key, new_master_password: newPass })
				});
				const data = await res.json();
				if (!res.ok) throw new Error(data.detail || 'Recovery failed');
				showToast('Vault recovered and unlocked!');
				refreshStatus();
			} catch (err) {
				showToast(err.message, true);
			}
		});

		// Change Password
		document.getElementById('btnChangePass').addEventListener('click', async () => {
			const oldP = prompt('Enter CURRENT Master Password:');
			if (!oldP) return;
			const newP = prompt('Enter NEW Master Password (min 8 characters):');
			if (!newP || newP.length < 8) {
				alert('Password too short');
				return;
			}
			try {
				const res = await fetch('/api/vault/change_password/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ old_master_password: oldP, new_master_password: newP })
				});
				const data = await res.json();
				if (!res.ok) throw new Error(data.detail || 'Change failed');
				showToast('Master password changed successfully!');
			} catch (err) {
				showToast(err.message, true);
			}
		});

		// Rotate Key
		document.getElementById('btnRotateKey').addEventListener('click', async () => {
			if (!confirm('This will generate a new 256-bit VMEK and atomically re-encrypt all vault documents on physical storage. Proceed?')) return;
			const pass = prompt('Confirm with Master Password:');
			if (!pass) return;
			try {
				const res = await fetch('/api/vault/rotate_key/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ master_password: pass })
				});
				const data = await res.json();
				if (!res.ok) throw new Error(data.detail || 'Rotation failed');
				showToast('Encryption key rotated! ' + data.reencrypted_documents + ' documents re-encrypted.');
				refreshStatus();
			} catch (err) {
				showToast(err.message, true);
			}
		});

		// Init
		refreshStatus();
	</script>
</body>
</html>`

	_, _ = w.Write([]byte(html))
}
