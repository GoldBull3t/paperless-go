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
