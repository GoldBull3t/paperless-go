# Modelo de Segurança e Recuperação do Vault Criptográfico

## 1. Visão Geral e Arquitetura

O **Cryptographic Vault** do `paperless-go` disponibiliza uma camada isolada de proteção para documentos de elevada confidencialidade (e.g. contratos confidenciais, documentos de identidade, declarações fiscais). 

Ao contrário de abordagens ingénuas baseadas numa simples flag na base de dados ou na cifragem simétrica direta usando a password do utilizador, o `paperless-go` implementa um modelo de **Envelope Encryption** com separação criptográfica estrita entre **autenticação** e **cifragem de dados**.

```
[Master Password] + [Cryptographic Salt (16B)]
              │
              ▼ (Argon2id: 3 iterations, 64MB memory, 2 threads)
      [Master Derived Key (32B)]
              │
    ┌─────────┴────────────────────────┐
    ▼ (HKDF-Expand: "auth-key")        ▼ (HKDF-Expand: "enc-key")
[Auth Key (32B)]                 [Key Encryption Key / KEK (32B)]
    │                                  │
    ▼ (SHA-256)                        ▼ (AES-256-GCM Unseal)
[Verifier Hash]                  [Vault Master Encryption Key / VMEK (32B)]
(armazenado na DB)               (mantido em RAM apenas enquanto desbloqueado)
                                       │
                                       ▼ (AES-256-GCM)
                            [Ficheiros de Documentos no Disco]
                            (Cabeçalho: PPLX:VAULT:1\n + Nonce + Ciphertext)
```

---

## 2. Princípios Criptográficos Fundamentais

### A. Derivação de Chaves com Argon2id
- **Algoritmo:** Argon2id (RFC 9106), o padrão recomendado pela IETF e OWASP Password Storage Cheat Sheet.
- **Parâmetros:**
  - Iterações de tempo: `3`
  - Memória: `64 MB`
  - Paralelismo: `2 threads`
  - Tamanho da chave de saída: `32 bytes` (256 bits).
- Proporciona elevada resistência contra ataques de dicionário e aceleração de força bruta em GPUs e ASICs.

### B. Separação Estrita entre Autenticação e Cifragem
- O hash guardado na base de dados (`verifier_hash`) é calculado sobre uma sub-chave de autenticação (`authKey`), gerada via **HKDF** (RFC 5869) com a etiqueta de domínio `paperless-vault-auth-key-v1`.
- A chave que decifra a VMEK (`encKey`) é gerada com a etiqueta de domínio `paperless-vault-encryption-key-v1`.
- **Garantia matemática:** Mesmo na eventualidade de comprometimento integral da base de dados e extração do `verifier_hash`, é matematicamente impossível calcular ou inferir a chave de decifração (`encKey`) sem a palavra-passe original.

### C. Envelope Encryption (VMEK)
- Os documentos protegidos são cifrados com uma chave aleatória de 256 bits gerada criptograficamente via CSPRNG (`crypto/rand`), denominada **Vault Master Encryption Key (VMEK)**.
- A VMEK é armazenada na base de dados em formato encapsulado (`wrapped_key_hex`) cifrada com a `encKey` utilizando **AES-256-GCM** (Galois/Counter Mode com autenticação de integridade de 128 bits e nonces de 96 bits únicos por operação).

---

## 3. Gestão e Rotação de Chaves

### A. Alteração da Master Password (Sem Re-cifragem no Disco)
- Endpoint: `POST /api/vault/change_password/`
- Como a VMEK é desacoplada da password do utilizador, a alteração da master password apenas decifra a VMEK em memória, deriva um novo par de chaves Argon2id/HKDF com um novo sal, e re-encapsula a VMEK na base de dados.
- **Impacto I/O:** Instantâneo. Não é necessário re-cifrar gigabytes de documentos em disco.

### B. Rotação Integral da VMEK (Key Rotation com Re-cifragem)
- Endpoint: `POST /api/vault/rotate_key/`
- Gera uma nova `VMEK_v2`.
- Decifra os ficheiros físicos no disco (originais e ficheiros de arquivo) com `VMEK_v1` e re-cifra-os atomicamente com `VMEK_v2`.
- Remove e destrói de forma segura (*shredding* com sobreescrita de bytes aleatórios e zeros) os ficheiros temporários intermédios.
- Incrementa `key_version` e `vault_key_version`.
- Zeroiza `VMEK_v1` da memória RAM.

---

## 4. Bloqueio Automático (Auto-Lock) e Destruição em Memória

- **Timeout Configurável:** O vault suporta fecho automático após inatividade (por defeito `15 minutos`).
- **Zeroização Ativa de Memória:** Quando o vault é bloqueado (via timeout ou chamada explícita a `POST /api/vault/lock/`):
  - A fatia de bytes contendo a VMEK em RAM é ativamente sobreescrita com zeros (`zeroize(v.vmek)`).
  - O ponteiro é atribuído a `nil`.
  - Tentativas subsequentes de acesso a ficheiros resultam em `ErrVaultLocked` (HTTP `423 Locked`).
- **Thumbnails e Pré-visualizações:** Ao proteger um documento no vault, qualquer thumbnail previamente gerado na diretoria de cache de thumbnails é destruído com sobrescrita criptográfica (`crypto.ShredFile`), prevenindo fugas forenses de imagens reduzidas em disco.

---

## 5. Procedimento de Recuperação de Emergência (Emergency Kit)

### A. Como Funciona a Chave de Recuperação
Ao inicializar o vault pela primeira vez (`POST /api/vault/setup/`), o sistema devolve uma **Emergency Recovery Key** com 128 bits de entropia criptográfica formatada em grupos de 4 caracteres hexadecimais:
```
XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX
```
- A VMEK é também encapsulada com uma chave derivada desta chave de emergência via Argon2id com sal independente (`recovery_wrapped_key_hex`).

### B. Procedimento de Recuperação em Caso de Perda da Password
1. Envie um pedido autenticado como superutilizador ao endpoint:
   ```http
   POST /api/vault/recover/
   Content-Type: application/json

   {
     "recovery_key": "XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX",
     "new_master_password": "NovaPasswordSegura2026!"
   }
   ```
2. O sistema decifra a VMEK armazenada com a chave de recuperação, valida a autenticidade dos dados, gera um novo sal e re-encapsula a VMEK com a nova palavra-passe.
3. O acesso ao cofre é imediatamente restabelecido com a nova palavra-passe.

---

## 6. Modelo de Ameaças e Limitações Forenses Realistas

> [!WARNING]
> **Aviso de Transparência e Engenharia de Segurança:**
> Em conformidade com as regras éticas e boas práticas de segurança, o `paperless-go` **nunca promete segurança absoluta nem imunidade forense total**. 
> Qualquer sistema informático está sujeito a restrições físicas e do sistema operativo subjacente.

### Limitações Conhecidas:
1. **Memória RAM no Estado Desbloqueado:**
   - Enquanto o cofre se encontrar desbloqueado (`unlocked`), a VMEK reside em memória RAM para permitir streaming eficiente de pré-visualizações e downloads.
   - Um atacante com privilégios de `root` ou capacidade de ler a memória física do processo (e.g. *cold boot attack*, despejo de memória do hypervisor, ou *dump* de `/proc/$PID/mem`) poderá extrair a VMEK da memória enquanto o vault estiver ativo.
2. **Swap e Paginação de Memória:**
   - Em sistemas operativos sem memória encriptada ou sem paginação desativada, fragmentos de memória contendo texto ou chaves podem temporariamente ser escritos na partição de swap pelo gestor de memória do kernel.
   - **Recomendação:** Implementar partição de swap encriptada (`dm-crypt`/LUKS) e ambientes de execução em contentor Docker isolado.
3. **Sistemas de Ficheiros Copy-on-Write (CoW):**
   - Em sistemas de ficheiros avançados como ZFS ou Btrfs, a sobreescrita de ficheiros (*shredding*) pode alocar um novo bloco em vez de sobreescrever os setores físicos antigos devido a cópia em escrita e snapshots.
   - **Recomendação:** Para conformidade estrita de descarte físico, encriptar todo o volume do disco de armazenamento subjacente.
4. **Metadados:**
   - O título, data de criação e metadados estruturais do documento continuam visíveis na base de dados SQLite indexados pelo FTS5 (a menos que a base de dados SQLite completa seja cifrada ao nível do volume do host). Apenas o acesso via API bloqueia a consulta de metadados quando o vault está bloqueado.

