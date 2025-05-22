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
	data      map[string]string
	masterKey []byte
	authedAt  time.Time
	mu        sync.Mutex
	cipherGCM cipher.AEAD
	nonceSize int
}

func New() *Vault {
	return &Vault{data: make(map[string]string)}
}

func (v *Vault) promptMaster() error {
	if time.Since(v.authedAt) < authCacheDuration && v.cipherGCM != nil {
		return nil
	}
	if _, err := os.Stat(FilePath()); os.IsNotExist(err) {
		// First-time setup: create new master key
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
		// Existing vault: prompt for master key
		for {
			fmt.Print("Enter MasterKey: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			v.initCipher(pw)
			if err := v.load(); err != nil {
				fmt.Println("Incorrect MasterKey. Try again.")
				continue
			}
			v.authedAt = time.Now()
			break
		}
	}
	return nil
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
