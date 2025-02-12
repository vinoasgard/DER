package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)
// Config struct untuk membaca config.json
type Config struct {
	DeleteSentEmails    bool   `json:"deleteSentEmails"`
	NewFileNameTemplate string `json:"newFileNameTemplate"`
	SubjectTemplate     string `json:"subjectTemplate"`
	ReplyToTemplate     string `json:"replyToTemplate"`
	FromName            string `json:"fromName"`
	EnableAttachment    bool   `json:"enableAttachment"`
	Letter              string `json:"letter"`
	ImageBase64         string `json:"ImageBase64"`
	Attachment          string `json:"attachment"`
}

// Variabel global
var (
	globalSentCount      int
	globalRemainingCount int
	successCount         int
	failureCount         int
	webAppLimitReached   = make(map[int]bool)
	config               Config
	proxyList            []string
	proxyIndex           int
	proxyMutex           sync.Mutex
)

// Fungsi untuk membaca file dan mengembalikan slice string
func readFile(fileName string) ([]string, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}
	return lines, scanner.Err()
}

// Fungsi untuk menghasilkan string acak
func generateRandomString(length int, charset string) string {
	var charPool string
	switch charset {
	case "alphanumeric":
		charPool = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	case "uppercase":
		charPool = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	case "lowercase":
		charPool = "abcdefghijklmnopqrstuvwxyz"
	case "numeric":
		charPool = "0123456789"
	default:
		charPool = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}

	rand.Seed(time.Now().UnixNano())
	result := make([]byte, length)
	for i := range result {
		result[i] = charPool[rand.Intn(len(charPool))]
	}
	return string(result)
}

// Fungsi untuk mengambil proxy dari format username:password:hostname:port
func getProxyFromLuna(proxyString string) (string, error) {
	proxyMutex.Lock()
	defer proxyMutex.Unlock()

	if len(proxyList) == 0 {
		return "", fmt.Errorf("no proxies available")
	}

	proxy := proxyList[proxyIndex]
	proxyIndex = (proxyIndex + 1) % len(proxyList) // Rotasi proxy

	return proxy, nil
}

// Fungsi untuk mengirim email dengan proxy
func sendEmail(webAppURL, email string, index int, wg *sync.WaitGroup, proxyString string) {
	defer wg.Done()

	if webAppLimitReached[index] {
		color.Yellow("WebApp #%d already rate-limited. Skipping email %s", index+1, email)
		return
	}

	// Generate dynamic subject and replyTo
	randomID := generateRandomString(7, "alphanumeric")
	randomUppercase := generateRandomString(5, "uppercase")
	randomLowercase := generateRandomString(4, "lowercase")
	randomNumber := generateRandomString(10, "numeric")

	subject := strings.NewReplacer(
		"{email}", email,
		"{randomID:7}", randomID,
		"{randomUppercase:5}", randomUppercase,
		"{randomLowercase:4}", randomLowercase,
		"{randomNumber:10}", randomNumber,
	).Replace(config.SubjectTemplate)

	replyTo := strings.NewReplacer(
		"{email}", email,
		"{randomID:7}", randomID,
		"{randomUppercase:5}", randomUppercase,
		"{randomLowercase:4}", randomLowercase,
		"{randomNumber:10}", randomNumber,
	).Replace(config.ReplyToTemplate)

	// Ambil proxy dari string proxyString
	proxy, err := getProxyFromLuna(proxyString)
	if err != nil {
		color.Red("Failed to get proxy: %v", err)
		failureCount++
		return
	}

	// Parsing proxy string ke dalam komponen username, password, hostname, port
	proxyParts := strings.Split(proxy, ":")
	if len(proxyParts) != 4 {
		color.Red("Invalid proxy format: %s", proxy)
		failureCount++
		return
	}

	username := proxyParts[0]
	password := proxyParts[1]
	hostname := proxyParts[2]
	port := proxyParts[3]

	// Setup proxy
	proxyUrl, err := url.Parse(fmt.Sprintf("http://%s:%s@%s:%s", username, password, hostname, port))
	if err != nil {
		color.Red("Failed to parse proxy URL: %v", err)
		failureCount++
		return
	}

	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyUrl)},
	}

	// Prepare request
	req, err := http.NewRequest("GET", webAppURL, nil)
	if err != nil {
		log.Printf("Failed to create request for %s: %v", email, err)
		failureCount++
		return
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("to", email)
	q.Add("subject", subject)
	q.Add("fromName", config.FromName)
	q.Add("replyTo", replyTo)
	q.Add("attachment", fmt.Sprintf("%t", config.EnableAttachment))
	q.Add("newFileName", generateRandomString(10, "alphanumeric"))
	q.Add("htmlFileId", config.Letter)
	q.Add("imageFileId", config.ImageBase64)
	q.Add("attachmentFileId", config.Attachment)
	req.URL.RawQuery = q.Encode()

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send email to %s: %v", email, err)
		failureCount++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		color.Red("WebApp #%d rate limit reached", index+1)
		webAppLimitReached[index] = true
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to send email to %s: %s", email, resp.Status)
		failureCount++
		return
	}

	successCount++
	globalSentCount++
	globalRemainingCount--
	color.Green("Email sent to: %s using proxy: %s", email, proxy)
}

// Fungsi utama
func main() {
	// Baca config.json
	configFile, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("Failed to read config.json: %v", err)
	}
	if err := json.Unmarshal(configFile, &config); err != nil {
		log.Fatalf("Failed to parse config.json: %v", err)
	}

	// Baca daftar email
	emailList, err := readFile("list.txt")
	if err != nil {
		log.Fatalf("Failed to read email list: %v", err)
	}
	globalRemainingCount = len(emailList)

	// Baca Web App URLs
	webAppURLs, err := readFile("smtp.txt")
	if err != nil {
		log.Fatalf("Failed to read smtp.txt: %v", err)
	}

	// Daftar proxy dari string username:password:hostname:port
	proxyList = []string{
		"user-smcrew-region-us:Waras123:as.o0u389rs.lunaproxy.net:12233",
		// Tambahkan lebih banyak proxy jika diperlukan
	}

	// Kirim email secara konkuren
	var wg sync.WaitGroup

	for i, webAppURL := range webAppURLs {
		for _, email := range emailList {
			wg.Add(1)
			go sendEmail(webAppURL, email, i, &wg, proxyList[0]) // Gunakan proxy pertama
		}
	}
	wg.Wait()

	// Tampilkan recap
	color.Blue("\n=== Email Delivery Recap ===")
	color.Green("Total Success: %d", successCount)
	color.Red("Total Failed: %d", failureCount)
	color.Yellow("Total Undelivered: %d", globalRemainingCount)
	color.Blue("Sent By ./MrGuest404 Sender.")
}