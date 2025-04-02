# Outlook Email Delta Sync & Attachment Scanner

This repository contains a Go-based prototype that uses the Microsoft Graph API and the OpenAI Chat API to synchronize Outlook emails incrementally (using the delta API), scan attachments for sensitive content, and flag emails that contain sensitive data.

> **Note:** This is a proof-of-concept script. In production, you should implement robust error handling, token refresh mechanisms, and secure storage for credentials.

---

## Features

- **OAuth2 Authentication:**  
  Authenticate users via Microsoft and obtain an access token.

- **Delta Synchronization:**  
  Use Microsoft Graph delta queries to incrementally synchronize newly created emails from the Inbox without re-fetching the entire message set.

- **Attachment Processing:**  
  Retrieve attachments from emails. If the attachment is a text file (CSV, TXT, or text MIME type), the script decodes the content—either from the embedded base64 data or via a raw GET request to the `/$value` endpoint.

- **Sensitive Data Check:**  
  Leverage the OpenAI Chat API (using the new chat completions endpoint) to analyze attachment content for sensitive information (e.g., personal names, addresses, phone numbers).

- **Email Flagging:**  
  If sensitive data is detected, the email is flagged via the Graph API.

---

## Prerequisites

- [Go](https://golang.org/dl/) (version 1.16 or later)
- Microsoft 365 account with access to Outlook mail data.
- An app registered in [Azure Active Directory](https://portal.azure.com) with the following permissions:
  - Delegated: `User.Read`, `Mail.Read`, `Mail.ReadWrite`
- A valid OpenAI API key with access to the chat completions endpoint.

---

## Environment Variables

Before running the script, set the following environment variables:

- **MS_CLIENT_ID**: Your Microsoft application (client) ID.
- **MS_CLIENT_SECRET**: Your Microsoft application client secret.
- **OPENAI_API_KEY**: Your OpenAI API key.

For example, on Linux/macOS, you can set them in your shell:

```bash
export MS_CLIENT_ID="your-microsoft-client-id"
export MS_CLIENT_SECRET="your-microsoft-client-secret"
export OPENAI_API_KEY="your-openai-api-key"
```

---

## Setup & Running

1. **Clone the Repository**

   ```bash
   git clone https://github.com/yourusername/your-repo.git
   cd your-repo
   ```

2. **Build and Run the Script**

   Run the script using the Go command:

   ```bash
   go run main.go
   ```

3. **Login via Browser**

   Open a browser and navigate to [http://localhost:8080](http://localhost:8080). Click on the **Login** button to sign in with your Microsoft account. The script will obtain an access token after successful authentication.

4. **Background Processing**

   After login, the script will begin polling your Outlook Inbox using the delta endpoint. It will fetch newly created emails, retrieve any attachments, analyze them using the OpenAI API, and flag emails with sensitive content.

---

## How It Works

1. **OAuth2 Flow:**
   - The `/login` endpoint redirects the user to Microsoft's OAuth2 consent screen.
   - The `/callback` endpoint processes the returned authorization code, exchanges it for an access token, and stores it globally.

2. **Delta Synchronization:**
   - The `emailPoller` function runs in a background goroutine. It calls `fetchDeltaEmails`, which initially uses a full snapshot query (`delta?changeType=created&$top=10`) and saves the returned `@odata.deltaLink`.
   - On subsequent polls, the saved `deltaLink` is used to fetch only incremental changes (newly created emails).

3. **Attachment Handling:**
   - For each email with attachments, the script calls `fetchAttachments` to retrieve attachment metadata.
   - The function `getAttachmentContent` decodes the content from `contentBytes` if available; otherwise, it fetches the raw content from the `/$value` endpoint.

4. **Sensitive Data Check:**
   - The attachment content is sent to the OpenAI Chat API via `checkSensitiveData`. The API is prompted to determine whether the text contains personal or sensitive information.
   - If sensitive data is detected, the email is flagged using the `flagEmail` function.

---

## Customization

- **Polling Interval:**  
  The script currently polls every 30 seconds. Adjust the sleep duration in the `emailPoller` function as needed.

- **Model & API Settings:**  
  The OpenAI Chat API is configured to use the `gpt-4o-mini` model. You can change this and other parameters (e.g., `MaxTokens`, `Temperature`) in the `checkSensitiveData` function.

- **Error Handling:**  
  Additional error handling, logging, and recovery strategies should be added for production use.

---

## Disclaimer

This project is provided "as-is" without warranty of any kind. Use this code at your own risk and ensure proper security measures and error handling before deploying to production environments.

