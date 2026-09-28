// Command probe is a set of one-shot tests for a real Kindle.
//
//	probe [url]          HTTPS test: can a static Go binary reach Hardcover?
//	probe events [flags] log file and LIPC events (what can trigger a sync?)
//	probe ids [flags]    list ISBN / ASIN found inside the book files
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/certs"
)

const metadataURL = "https://api.hardcover.app/.well-known/oauth-authorization-server"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "events":
			os.Exit(events(os.Args[2:]))
		case "ids":
			os.Exit(ids(os.Args[2:]))
		}
	}
	httpsTest()
}

func httpsTest() {
	fmt.Printf("go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("time: %s\n", time.Now().UTC().Format(time.RFC3339))

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: certs.Pool()},
		},
	}

	url := metadataURL
	if len(os.Args) > 1 {
		url = os.Args[1] // for local tests only
	}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("https: FAIL: %v\n", err)
		os.Exit(2)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		fmt.Printf("https: FAIL reading body: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("https: status %d, tls %s\n", resp.StatusCode, tls.VersionName(resp.TLS.Version))

	var meta struct {
		DeviceEndpoint string   `json:"device_authorization_endpoint"`
		TokenEndpoint  string   `json:"token_endpoint"`
		Scopes         []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		fmt.Printf("https: FAIL parsing JSON: %v\n", err)
		os.Exit(3)
	}
	fmt.Printf("device_endpoint: %s\n", meta.DeviceEndpoint)
	fmt.Printf("token_endpoint: %s\n", meta.TokenEndpoint)
	fmt.Printf("scopes: %v\n", meta.Scopes)
	if resp.StatusCode != http.StatusOK {
		os.Exit(4)
	}
	fmt.Println("result: OK")
}
