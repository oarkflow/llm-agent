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
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh/terminal"
)

var vaultDir = "./.vault"

const (
	storageFile       = "vault.db"
	authCacheDuration = time.Minute
)

func init() {
	if err := initStorage(); err != nil {
		log.Fatal(err)
	}
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
			return fmt.Errorf("Error creating .vault directory:", err)
		}
	}
	return nil
}

// Vault holds encrypted secrets on disk
type Vault struct {
	data      map[string]string
	masterKey []byte
	authedAt  time.Time
	mu        sync.Mutex
	cipherGCM cipher.AEAD
	nonceSize int
}

// NewVault initializes an empty Vault instance
func NewVault() *Vault {
	return &Vault{data: make(map[string]string)}
}

// promptMaster ensures master key is set/loaded and cached
func (v *Vault) promptMaster() error {
	// Removed internal lock; caller must hold lock.
	// If already authenticated recently, skip
	if time.Since(v.authedAt) < authCacheDuration && v.cipherGCM != nil {
		return nil
	}
	// Check if vault file exists
	_, err := os.Stat(FilePath())
	if os.IsNotExist(err) {
		// New vault setup: prompt for new MasterKey with clear message
		for {
			fmt.Println("Vault database not found. Setting up a new vault. Please set a new MasterKey.")
			fmt.Print("Enter new MasterKey: ")
			pw1, err := terminal.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			fmt.Print("Confirm new MasterKey: ")
			pw2, err := terminal.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			if string(pw1) != string(pw2) {
				fmt.Println("MasterKeys do not match. Try again.")
				continue
			}
			key := deriveKey(pw1)
			block, err := aes.NewCipher(key)
			if err != nil {
				return err
			}
			gcm, err := cipher.NewGCM(block)
			if err != nil {
				return err
			}
			v.masterKey = key
			v.cipherGCM = gcm
			v.nonceSize = gcm.NonceSize()
			// save empty vault
			if err := v.save(); err != nil {
				return err
			}
			v.authedAt = time.Now()
			return nil
		}
	} else if err != nil {
		return err
	}
	// Existing vault: prompt for password to enter
	fmt.Print("Enter master key: ")
	pw, err := terminal.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}
	key := deriveKey(pw)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	v.masterKey = key
	v.cipherGCM = gcm
	v.nonceSize = gcm.NonceSize()
	if err := v.load(); err != nil {
		v.cipherGCM = nil // reset
		return fmt.Errorf("failed to decrypt vault: %w", err)
	}
	v.authedAt = time.Now()
	return nil
}

// deriveKey pads/truncates password to 32 bytes
func deriveKey(pw []byte) []byte {
	key := make([]byte, 32)
	n := copy(key, pw)
	if n < 32 {
		copy(key[n:], []byte(strings.Repeat("0", 32-n)))
	}
	return key
}

// load decrypts and loads vault data
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

// save encrypts and persists vault data
func (v *Vault) save() error {
	plain, err := json.Marshal(v.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, v.nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := v.cipherGCM.Seal(nonce, nonce, plain, nil)
	enc := base64.StdEncoding.EncodeToString(ciphertext)
	return os.WriteFile(FilePath(), []byte(enc), 0600)
}

// Set adds or updates a secret
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

func Execute() {
	vault := NewVault()
	// pre-authenticate vault in a locked region
	vault.mu.Lock()
	err := vault.promptMaster()
	vault.mu.Unlock()
	if err != nil {
		log.Fatal("Authentication failed:", err)
	}
	// Start REST API in background
	go func() {
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
				body, _ := ioutil.ReadAll(r.Body)
				if err := vault.Set(key, string(body)); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			case http.MethodDelete:
				if err := vault.Delete(key); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
		log.Println("REST API running on :8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	// CLI loop
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("vault> ")
		if !scanner.Scan() {
			break
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			fmt.Println("usage: set|get|delete key [value]")
			continue
		}
		op, key := strings.ToLower(parts[0]), parts[1]
		switch op {
		case "set", "update":
			fmt.Print("Enter secret: ")
			pw, _ := terminal.ReadPassword(int(os.Stdin.Fd()))
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
		case "exit":
			return
		default:
			fmt.Println("unknown command")
		}
	}
}
