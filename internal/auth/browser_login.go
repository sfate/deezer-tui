package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const deezerLoginURL = "https://www.deezer.com/login?redirect=%2Faccount"

var ErrBrowserNotFound = errors.New("supported browser not found")

type BrowserLoginOptions struct {
	Timeout time.Duration
}

func BrowserLogin(ctx context.Context, opts BrowserLoginOptions) (string, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	port, err := freePort()
	if err != nil {
		return "", err
	}
	profileDir, err := os.MkdirTemp("", "deezer-tui-login-*")
	if err != nil {
		return "", fmt.Errorf("create browser profile: %w", err)
	}
	defer func() { _ = os.RemoveAll(profileDir) }()

	browser, err := browserCommand(profileDir, port)
	if err != nil {
		return "", err
	}
	if err := browser.Start(); err != nil {
		return "", fmt.Errorf("open browser login: %w", err)
	}
	defer func() {
		if browser.Process != nil {
			_ = browser.Process.Kill()
			_, _ = browser.Process.Wait()
		}
	}()

	wsURL, err := waitForDebugger(ctx, port)
	if err != nil {
		return "", err
	}
	return waitForARLCookie(ctx, wsURL)
}

func browserCommand(profileDir string, port int) (*exec.Cmd, error) {
	browser, err := findChromiumBrowser()
	if err != nil {
		return nil, err
	}
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate",
		"--user-data-dir=" + profileDir,
		deezerLoginURL,
	}
	return exec.Command(browser, args...), nil
}

func findChromiumBrowser() (string, error) {
	candidates := browserCandidates()
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", ErrBrowserNotFound
}

func browserCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"google-chrome",
			"chromium",
			"brave-browser",
		}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		return []string{
			filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
			"chrome",
			"msedge",
			"brave",
		}
	default:
		return []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"brave-browser",
			"microsoft-edge",
		}
	}
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find browser debug port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForDebugger(ctx context.Context, port int) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		wsURL, err := pageDebuggerURL(url)
		if err == nil && wsURL != "" {
			return wsURL, nil
		}
		select {
		case <-ctx.Done():
			return "", errors.New("timed out waiting for browser login session")
		case <-ticker.C:
		}
	}
}

func pageDebuggerURL(url string) (string, error) {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var payload []struct {
		Type                 string `json:"type"`
		URL                  string `json:"url"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	for _, target := range payload {
		if target.Type == "page" && strings.Contains(target.URL, "deezer.com") && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	for _, target := range payload {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", nil
}

func waitForARLCookie(ctx context.Context, wsURL string) (string, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return "", fmt.Errorf("connect browser login session: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	requestID := 0
	for {
		requestID++
		arl, err := readARLCookie(conn, requestID)
		if err == nil && arl != "" {
			return arl, nil
		}
		select {
		case <-ctx.Done():
			return "", errors.New("timed out waiting for Deezer login")
		case <-ticker.C:
		}
	}
}

func readARLCookie(conn *websocket.Conn, requestID int) (string, error) {
	if err := conn.WriteJSON(map[string]any{
		"id":     requestID,
		"method": "Network.getAllCookies",
	}); err != nil {
		return "", err
	}

	for {
		var response cdpCookieResponse
		if err := conn.ReadJSON(&response); err != nil {
			return "", err
		}
		if response.ID != requestID {
			continue
		}
		if response.Error != nil {
			return "", errors.New(response.Error.Message)
		}
		return arlFromCookies(response.Result.Cookies), nil
	}
}

type cdpCookieResponse struct {
	ID     int `json:"id"`
	Result struct {
		Cookies []cdpCookie `json:"cookies"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type cdpCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

func arlFromCookies(cookies []cdpCookie) string {
	for _, cookie := range cookies {
		if cookie.Name != "arl" {
			continue
		}
		if !strings.Contains(cookie.Domain, "deezer.com") {
			continue
		}
		if value := strings.TrimSpace(cookie.Value); value != "" {
			return value
		}
	}
	return ""
}
