package main

import (
	"bufio"
	"crypto/tls"
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

// Config struct for reading config.json
type Config struct {
	DeleteSentEmails    bool     `json:"deleteSentEmails"`
	NewFileNameTemplate string   `json:"newFileNameTemplate"`
	SubjectTemplate     string   `json:"subjectTemplate"`
	ReplyToTemplate     string   `json:"replyToTemplate"`
	FromName            string   `json:"fromName"`
	EnableAttachment    bool     `json:"enableAttachment"`
	Letter              string   `json:"letter"`
	ImageBase64         string   `json:"ImageBase64"`
	Attachment          string   `json:"attachment"`
	RandomDomain        []string `json:"randomDomain"`
}

// Global variables
var (
	globalSentCount      int
	globalRemainingCount int
	successCount         int
	failureCount         int
	webAppLimitReached   = make(map[int]bool)
	config               Config
)

// Function to read a file and return a slice of strings
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

// Function to generate a random string
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

func getRandomDomain() (string, error) {
	if len(config.RandomDomain) == 0 {
		return "", fmt.Errorf("no random domains available")
	}
	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(len(config.RandomDomain))
	return config.RandomDomain[randomIndex], nil
}

// Function to send email
func sendEmail(webAppURL, email string, index int, wg *sync.WaitGroup) {
	defer wg.Done()

	if webAppLimitReached[index] {
		color.Yellow("WebApp #%d already rate-limited. Skipping email %s", index+1, email)
		return
	}

	randomDomain, err := getRandomDomain()
	if err != nil {
		color.Red("Failed to get random domain for %s: %v", email, err)
		failureCount++
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
		"{randomDomain}", randomDomain,
	).Replace(config.SubjectTemplate)

	replyTo := strings.NewReplacer(
		"{email}", email,
		"{randomID:7}", randomID,
		"{randomUppercase:5}", randomUppercase,
		"{randomLowercase:4}", randomLowercase,
		"{randomNumber:10}", randomNumber,
		"{randomDomain}", randomDomain,
	).Replace(config.ReplyToTemplate)

	// Disable SSL verification (for testing purposes only)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{
		Transport: tr,
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
	q.Add("randomDomain", randomDomain)
	req.URL.RawQuery = q.Encode()

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		// Fallback to HTTP if HTTPS fails
		if strings.Contains(err.Error(), "server gave HTTP response to HTTPS client") {
			color.Yellow("HTTPS failed, falling back to HTTP for %s", email)
			webAppURL = strings.Replace(webAppURL, "https://", "http://", 1)
			req.URL, _ = url.Parse(webAppURL)
			resp, err = client.Do(req)
			if err != nil {
				log.Printf("Failed to send email to %s: %v", email, err)
				failureCount++
				return
			}
		} else {
			log.Printf("Failed to send email to %s: %v", email, err)
			failureCount++
			return
		}
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
	color.Green("Email sent to: %s", email)
}

// Function to distribute emails among SMTP servers
func distributeEmails(emailList []string, webAppURLs []string) map[int][]string {
	distribution := make(map[int][]string)
	for i, email := range emailList {
		index := i % len(webAppURLs)
		distribution[index] = append(distribution[index], email)
	}
	return distribution
}

// Main function
func main() {
	// Read config.json
	configFile, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("Failed to read config.json: %v", err)
	}
	if err := json.Unmarshal(configFile, &config); err != nil {
		log.Fatalf("Failed to parse config.json: %v", err)
	}

	// Read email list
	emailList, err := readFile("list.txt")
	if err != nil {
		log.Fatalf("Failed to read email list: %v", err)
	}
	globalRemainingCount = len(emailList)

	// Read Web App URLs
	webAppURLs, err := readFile("smtp.txt")
	if err != nil {
		log.Fatalf("Failed to read smtp.txt: %v", err)
	}

	// Distribute emails among SMTP servers
	emailDistribution := distributeEmails(emailList, webAppURLs)

	// Send emails concurrently
	var wg sync.WaitGroup
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for i, webAppURL := range webAppURLs {
		wg.Add(1)
		go func(index int, url string) {
			defer wg.Done()
			for _, email := range emailDistribution[index] {
				<-ticker.C
				wg.Add(1)
				go sendEmail(url, email, index, &wg)
			}
		}(i, webAppURL)
	}
	wg.Wait()

	// Recap
	color.Blue("\n=== Email Delivery Recap ===")
	color.Green("Total Success: %d", successCount)
	color.Red("Total Failed: %d", failureCount)
	color.Yellow("Total Undelivered: %d", globalRemainingCount)
	color.Blue("Sent By ./MrGuest404 Sender.")
}
