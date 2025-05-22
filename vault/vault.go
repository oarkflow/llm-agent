package vault

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/oarkflow/llmagent/clipboard"
)

var (
	vaultDir     = "./.vault"
	defaultVault *Vault
)

const (
	storageFile       = "store.vlt"
	authCacheDuration = time.Minute
)

func init() {
	if err := initStorage(); err != nil {
		log.Fatal(err)
	}
	defaultVault = New()
}

func Get(key string) (string, error) {
	if defaultVault == nil {
		return "", fmt.Errorf("vault not initialized")
	}
	return defaultVault.Get(key)
}

func FilePath() string {
	return filepath.Join(vaultDir, storageFile)
}

func initStorage() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("Error getting home directory: %v", err)
	}
	vaultDir = filepath.Join(homeDir, ".vault")
	if _, err := os.Stat(vaultDir); os.IsNotExist(err) {
		err = os.MkdirAll(vaultDir, 0700)
		if err != nil {
			return fmt.Errorf("Error creating .vault directory: %v", err)
		}
	}
	return nil
}

type Vault struct {
	data           map[string]string
	masterKey      []byte
	authedAt       time.Time
	mu             sync.Mutex
	cipherGCM      cipher.AEAD
	nonceSize      int
	resetAttempts  int       // count for reset code failures
	normalAttempts int       // count for normal master key failures
	bannedUntil    time.Time // ban period end time
	lockedForever  bool      // permanent lock flag
}

func New() *Vault {
	return &Vault{data: make(map[string]string)}
}

// sendResetEmail simulates sending an email with the reset code
func sendResetEmail(code string) {
	// In a real implementation, send email to the admin/user.
	fmt.Printf("Sending reset code %s to user's email...\n", code)
}

// resetMasterKey implements the reset procedure via email confirmation.
func (v *Vault) resetMasterKey() error {
	// For simplicity, we use a fixed code. In production, generate a secure random code.
	resetCode := "123456"
	sendResetEmail(resetCode)

	reader := bufio.NewReader(os.Stdin)
	for i := 0; i < 3; i++ {
		fmt.Print("Enter reset code: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == resetCode {
			// Correct code; prompt for new master key.
			for {
				fmt.Print("Enter new MasterKey: ")
				new1, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return err
				}
				fmt.Print("Confirm new MasterKey: ")
				new2, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return err
				}
				if string(new1) != string(new2) {
					fmt.Println("MasterKeys do not match. Try again.")
					continue
				}
				v.initCipher(new1)
				// Reset failure counters and ban status.
				v.resetAttempts = 0
				v.normalAttempts = 0
				v.bannedUntil = time.Time{}
				if err := v.save(); err != nil {
					return err
				}
				fmt.Println("MasterKey has been reset successfully.")
				return nil
			}
		} else {
			fmt.Println("Incorrect reset code.")
		}
	}
	// After 3 failed attempts, ban for 10 minutes.
	v.resetAttempts += 3
	v.bannedUntil = time.Now().Add(10 * time.Minute)
	fmt.Printf("Too many incorrect reset attempts. Vault is banned until %v.\n", v.bannedUntil)
	return fmt.Errorf("failed to reset MasterKey: vault banned until %v", v.bannedUntil)
}

func (v *Vault) promptMaster() error {
	// If already authenticated recently, skip
	if time.Since(v.authedAt) < authCacheDuration && v.cipherGCM != nil {
		return nil
	}
	// Check if vault is permanently locked
	if v.lockedForever {
		return fmt.Errorf("vault locked permanently")
	}
	// Check if vault is banned
	if !v.bannedUntil.IsZero() && time.Now().Before(v.bannedUntil) {
		return fmt.Errorf("vault banned until %v", v.bannedUntil)
	}
	// New vault setup if storage file doesn't exist.
	if _, err := os.Stat(FilePath()); os.IsNotExist(err) {
		for {
			fmt.Println("Vault database not found. Setting up a new vault.")
			fmt.Print("Enter new MasterKey: ")
			pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			fmt.Print("Confirm new MasterKey: ")
			pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			if string(pw1) != string(pw2) {
				fmt.Println("MasterKeys do not match. Try again.")
				continue
			}
			v.initCipher(pw1)
			if err := v.save(); err != nil {
				return err
			}
			v.authedAt = time.Now()
			return nil
		}
	} else {
		// Existing vault: let user choose reset procedure or normal entry.
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Do you want to reset MasterKey? (y/N): ")
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans == "y" {
			if err := v.resetMasterKey(); err != nil {
				return err
			}
			// Reset successful; proceed.
			v.authedAt = time.Now()
			return nil
		}
		// Normal master key input loop.
		for {
			if v.lockedForever {
				return fmt.Errorf("vault locked permanently")
			}
			if !v.bannedUntil.IsZero() && time.Now().Before(v.bannedUntil) {
				return fmt.Errorf("vault banned until %v", v.bannedUntil)
			}
			fmt.Print("Enter MasterKey: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			v.initCipher(pw)
			if err := v.load(); err != nil {
				fmt.Println("Incorrect MasterKey.")
				v.normalAttempts++
				if v.normalAttempts >= 3 {
					// If already banned, lock permanently.
					if !v.bannedUntil.IsZero() && time.Now().After(v.bannedUntil) {
						v.lockedForever = true
						return fmt.Errorf("vault locked permanently due to repeated failures")
					}
					// Otherwise, enforce a ban.
					v.bannedUntil = time.Now().Add(10 * time.Minute)
					fmt.Printf("Too many attempts. Vault is banned until %v.\n", v.bannedUntil)
				}
				continue
			}
			v.authedAt = time.Now()
			// Reset normal attempt counter after a success.
			v.normalAttempts = 0
			return nil
		}
	}
}

func (v *Vault) initCipher(pw []byte) {
	key := deriveKey(pw)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	v.masterKey = key
	v.cipherGCM = gcm
	v.nonceSize = gcm.NonceSize()
}

func deriveKey(pw []byte) []byte {
	key := make([]byte, 32)
	n := copy(key, pw)
	if n < 32 {
		copy(key[n:], []byte(strings.Repeat("0", 32-n)))
	}
	return key
}

func (v *Vault) load() error {
	enc, err := os.ReadFile(FilePath())
	if err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(string(enc))
	if err != nil {
		return err
	}
	nonce := data[:v.nonceSize]
	ciphertext := data[v.nonceSize:]
	plain, err := v.cipherGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, &v.data)
}

func (v *Vault) save() error {
	plain, err := json.Marshal(v.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, v.nonceSize)
	_, _ = io.ReadFull(rand.Reader, nonce)
	ciphertext := v.cipherGCM.Seal(nonce, nonce, plain, nil)
	enc := base64.StdEncoding.EncodeToString(ciphertext)
	return os.WriteFile(FilePath(), []byte(enc), 0600)
}

// Set stores or updates a secret
func (v *Vault) Set(key, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.promptMaster(); err != nil {
		return err
	}
	v.data[key] = value
	return v.save()
}

// Get retrieves a secret
func (v *Vault) Get(key string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.promptMaster(); err != nil {
		return "", err
	}
	val, ok := v.data[key]
	if !ok {
		return "", fmt.Errorf("key %s not found", key)
	}
	return val, nil
}

// Delete removes a secret
func (v *Vault) Delete(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.promptMaster(); err != nil {
		return err
	}
	delete(v.data, key)
	return v.save()
}

// Copy retrieves a secret and copies it to clipboard
func (v *Vault) Copy(key string) error {
	val, err := v.Get(key)
	if err != nil {
		return err
	}
	return clipboard.WriteAll(val)
}

// Execute starts CLI and HTTP server
func Execute() {
	vault := New()
	_ = vault.promptMaster()
	go startHTTP(vault)
	cliLoop(vault)
}

func startHTTP(vault *Vault) {
	http.HandleFunc("/vault/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/vault/")
		switch r.Method {
		case http.MethodGet:
			val, err := vault.Get(key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			fmt.Fprintln(w, val)
		case http.MethodPost, http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			_ = vault.Set(key, string(body))
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			_ = vault.Delete(key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func cliLoop(vault *Vault) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("vault> ")
		if !scanner.Scan() {
			break
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			fmt.Println("usage: set|get|delete|copy key [value]")
			continue
		}
		op, key := strings.ToLower(parts[0]), parts[1]
		switch op {
		case "set", "update":
			fmt.Print("Enter secret: ")
			pw, _ := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err := vault.Set(key, string(pw)); err != nil {
				fmt.Println("error:", err)
			}
		case "get":
			val, err := vault.Get(key)
			if err != nil {
				fmt.Println("error:", err)
			} else {
				fmt.Println(val)
			}
		case "delete":
			if err := vault.Delete(key); err != nil {
				fmt.Println("error:", err)
			}
		case "copy":
			if err := vault.Copy(key); err != nil {
				fmt.Println("error:", err)
			} else {
				fmt.Println("secret copied to clipboard")
			}
		case "exit":
			return
		default:
			fmt.Println("unknown command")
		}
	}
}
