package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var oauth2Config = &oauth2.Config{
	ClientID:     os.Getenv("MS_CLIENT_ID")
	ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
	RedirectURL:  "http://localhost:8080/callback",
	Endpoint: oauth2.Endpoint{
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	},
	Scopes: []string{
		"User.Read",
		"Mail.Read",
		"Mail.ReadWrite",
	},
}

var graphToken *oauth2.Token
var deltaLink string

var openaiAPIKey string

type Message struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
}

type Attachment struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	ContentBytes string `json:"contentBytes,omitempty"`
}

type DeltaResponse struct {
	OdataDeltaLink string    `json:"@odata.deltaLink"`
	OdataNextLink  string    `json:"@odata.nextLink"`
	Value          []Message `json:"value"`
}

type OpenAIRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

type OpenAIResponseChoice struct {
	Text string `json:"text"`
}

type OpenAIResponse struct {
	Choices []OpenAIResponseChoice `json:"choices"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
}

func main() {
	openaiAPIKey = os.Getenv("OPENAI_API_KEY")
	if openaiAPIKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/callback", callbackHandler)

	go emailPoller()

	log.Println("Server started on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if graphToken == nil {
		html := `<html>
					<head>
						<title>Login with Microsoft</title>
					</head>
					<body>
						<h1>Login with Microsoft</h1>
						<a href="/login">Login</a>
					</body>
				</html>`
		w.Write([]byte(html))
	} else {
		html := `<html>
					<head>
						<title>Logged In</title>
					</head>
					<body>
						<h1>Logged in with Microsoft</h1>
						<p>Email polling and attachment scanning are running in the background.</p>
					</body>
				</html>`
		w.Write([]byte(html))
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	url := oauth2Config.AuthCodeURL("state", oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusFound)
}

func callbackHandler(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "No code in request", http.StatusBadRequest)
		return
	}

	token, err := oauth2Config.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	graphToken = token
	log.Printf("Obtained access token: %s\n", token.AccessToken)
	http.Redirect(w, r, "/", http.StatusFound)
}

func emailPoller() {
	for {
		if graphToken == nil {
			log.Println("No token available. Waiting for user login...")
			time.Sleep(10 * time.Second)
			continue
		}

		emails, err := fetchDeltaEmails(graphToken.AccessToken)
		if err != nil {
			log.Printf("Error fetching emails: %v\n", err)
		} else {
			for _, email := range emails {
				attachments, err := fetchAttachments(graphToken.AccessToken, email.ID)
				if err != nil {
					log.Printf("Error fetching attachments for email %s: %v\n", email.ID, err)
					continue
				}
				shouldFlag := false
				for _, att := range attachments {
					if isTextAttachment(att) {
						content, err := getAttachmentContent(graphToken.AccessToken, email.ID, att)
						if err != nil {
							log.Printf("Error processing attachment %s: %v\n", att.ID, err)
							continue
						}

						if checkSensitiveData(content, openaiAPIKey) {
							shouldFlag = true
							break
						}
					}
				}
				if shouldFlag {
					err = flagEmail(graphToken.AccessToken, email.ID)
					if err != nil {
						log.Printf("Error flagging email %s: %v\n", email.ID, err)
					} else {
						log.Printf("Email %s flagged due to sensitive attachment data.\n", email.ID)
					}
				}
			}
		}

		time.Sleep(30 * time.Second)
	}
}

func isTextAttachment(att Attachment) bool {
	lowerName := strings.ToLower(att.Name)
	if strings.HasSuffix(lowerName, ".csv") || strings.HasSuffix(lowerName, ".txt") {
		return true
	}
	if strings.HasPrefix(att.ContentType, "text/") {
		return true
	}
	return false
}

func fetchDeltaEmails(token string) ([]Message, error) {
	var allMessages []Message
	var url string
	if deltaLink == "" {
		url = "https://graph.microsoft.com/v1.0/me/mailFolders/Inbox/messages/delta?changeType=created&$top=10"
	} else {
		url = deltaLink
	}

	for url != "" {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		client := http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		var deltaResp DeltaResponse
		if err := json.Unmarshal(body, &deltaResp); err != nil {
			return nil, err
		}
		allMessages = append(allMessages, deltaResp.Value...)
		if deltaResp.OdataNextLink != "" {
			url = deltaResp.OdataNextLink
		} else if deltaResp.OdataDeltaLink != "" {
			// Save the deltaLink for the next round.
			deltaLink = deltaResp.OdataDeltaLink
			url = ""
		} else {
			url = ""
		}
	}
	return allMessages, nil
}

func fetchAttachments(token, messageID string) ([]Attachment, error) {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/messages/%s/attachments", messageID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Value []Attachment `json:"value"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Error response status: %d", resp.StatusCode)
		log.Printf("Error response body: %s", string(body))
		return nil, fmt.Errorf("Graph API returned status %d", resp.StatusCode)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Value, nil
}

func getAttachmentContent(token, messageID string, att Attachment) (string, error) {
	if att.ContentBytes != "" {
		decoded, err := base64.StdEncoding.DecodeString(att.ContentBytes)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/messages/%s/attachments/%s/$value", messageID, att.ID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to fetch raw attachment, status: %d, body: %s", resp.StatusCode, string(body))
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(rawBytes), nil
}

func checkSensitiveData(content, openaiKey string) bool {
	question := fmt.Sprintf("Does the following text contain personal information such as names, addresses, phone numbers, or any sensitive details? Answer Yes or No.\n\nText: %s", content)

	chatReq := ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []ChatMessage{
			{Role: "developer", Content: "You are a helpful assistant."},
			{Role: "user", Content: question},
		},
		MaxTokens:   5,
		Temperature: 0.0,
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		log.Printf("Error marshaling OpenAI request: %v", err)
		return false
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Error creating OpenAI request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+openaiKey)

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request to OpenAI: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("failed to fetch raw attachment, status: %d, body: %s", resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading OpenAI response: %v", err)
		return false
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		log.Printf("Error unmarshaling OpenAI response: %v", err)
		return false
	}

	if len(chatResp.Choices) > 0 {
		answer := strings.TrimSpace(chatResp.Choices[0].Message.Content)
		log.Printf("OpenAI response: %s", answer)
		if strings.EqualFold(answer, "Yes") || strings.Contains(strings.ToLower(answer), "yes") {
			return true
		}
	}
	return false
}

func flagEmail(token, messageID string) error {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/messages/%s", messageID)
	payload := map[string]interface{}{
		"flag": map[string]string{
			"flagStatus": "flagged",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("failed to flag email, status: %d, response: %s", resp.StatusCode, string(respBody))
}
