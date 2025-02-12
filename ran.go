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
	domainList           []string
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

// Fungsi untuk memilih domain secara acak dari domain.txt
func getRandomDomain() string {
	rand.Seed(time.Now().UnixNano())
	if len(domainList) == 0 {
		return "defaultdomain.com"
	}
	return domainList[rand.Intn(len(domainList))]
}

// Fungsi untuk mengambil proxy secara rotasi
func getProxyFromLuna() (string, error) {
	proxyMutex.Lock()
	defer proxyMutex.Unlock()

	if len(proxyList) == 0 {
		return "", fmt.Errorf("no proxies available")
	}

	proxy := proxyList[proxyIndex]
	proxyIndex = (proxyIndex + 1) % len(proxyList)

	return proxy, nil
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

	// Baca daftar domain dari domain.txt
	domainList, err = readFile("domain.txt")
	if err != nil {
		log.Fatalf("Failed to read domain.txt: %v", err)
	}

	// Daftar proxy
	proxyList = []string{
		"user-smcrew-region-us:Waras123:as.o0u389rs.lunaproxy.net:12233",
	}

	// Kirim email secara konkuren
	var wg sync.WaitGroup

	for i, webAppURL := range webAppURLs {
		for _, email := range emailList {
			wg.Add(1)
			go sendEmail(webAppURL, email, i, &wg)
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

// Fungsi untuk mengirim email
func sendEmail(webAppURL, email string, index int, wg *sync.WaitGroup) {
	defer wg.Done()

	if webAppLimitReached[index] {
		color.Yellow("WebApp #%d already rate-limited. Skipping email %s", index+1, email)
		return
	}

	randomDomain := getRandomDomain()
	randomEmail := email + "@" + randomDomain

	proxy, err := getProxyFromLuna()
	if err != nil {
		color.Red("Failed to get proxy: %v", err)
		failureCount++
		return
	}

	client := &http.Client{}

	req, err := http.NewRequest("GET", webAppURL, nil)
	if err != nil {
		log.Printf("Failed to create request for %s: %v", email, err)
		failureCount++
		return
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("to", randomEmail)
	q.Add("fromName", config.FromName)
	req.URL.RawQuery = q.Encode()

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send email to %s: %v", randomEmail, err)
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
		log.Printf("Failed to send email to %s: %s", randomEmail, resp.Status)
		failureCount++
		return
	}

	successCount++
	globalSentCount++
	globalRemainingCount--
	color.Green("Email sent to: %s using proxy: %s", randomEmail, proxy)
}
