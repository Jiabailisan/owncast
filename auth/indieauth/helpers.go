package indieauth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"github.com/pkg/errors"
	"golang.org/x/net/html"

	"github.com/owncast/owncast/utils"
)

func createAuthRequest(authDestination, userID, displayName, accessToken, baseServer string) (*Request, error) {
	authURL, err := url.Parse(authDestination)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse IndieAuth destination")
	}

	authEndpointURL, err := getAuthEndpointFromURL(authURL.String())
	if err != nil {
		return nil, errors.Wrap(err, "unable to get IndieAuth endpoint from destination URL")
	}

	baseServerURL, err := url.Parse(baseServer)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse local owncast base server URL")
	}

	callbackURL := *baseServerURL
	callbackURL.Path = "/api/auth/indieauth/callback"

	codeVerifier := randString(50)
	codeChallenge := createCodeChallenge(codeVerifier)
	state := randString(20)
	responseType := "code"
	clientID := baseServerURL.String() // Our local URL
	codeChallengeMethod := "S256"

	redirect := *authEndpointURL

	q := authURL.Query()
	q.Add("response_type", responseType)
	q.Add("client_id", clientID)
	q.Add("state", state)
	q.Add("code_challenge_method", codeChallengeMethod)
	q.Add("code_challenge", codeChallenge)
	q.Add("me", authURL.String())
	q.Add("redirect_uri", callbackURL.String())
	redirect.RawQuery = q.Encode()

	return &Request{
		Me:                 authURL,
		UserID:             userID,
		DisplayName:        displayName,
		CurrentAccessToken: accessToken,
		Endpoint:           authEndpointURL,
		ClientID:           baseServer,
		CodeVerifier:       codeVerifier,
		CodeChallenge:      codeChallenge,
		State:              state,
		Redirect:           &redirect,
		Callback:           &callbackURL,
		Timestamp:          time.Now(),
	}, nil
}

// The shared IndieAuth authentication service moved: indieauth.com has been
// unreliable for a long time, and indielogin.com is its successor. Sites that
// still advertise the old provider are redirected to the new one, and a domain
// that advertises no authorization endpoint at all is authenticated through the
// successor instead of failing outright.
const (
	legacyIndieAuthHost      = "indieauth.com"
	defaultIndieAuthProvider = "https://indielogin.com/auth"
)

// successorIndieAuthEndpoint rewrites an endpoint that points at the retired
// provider to its successor, keeping the path.
func successorIndieAuthEndpoint(endpoint *url.URL) *url.URL {
	if endpoint == nil || !strings.EqualFold(endpoint.Hostname(), legacyIndieAuthHost) {
		return endpoint
	}

	rewritten := *endpoint
	rewritten.Host = strings.Replace(endpoint.Host, endpoint.Hostname(), "indielogin.com", 1)
	if rewritten.Path == "" {
		rewritten.Path = "/auth"
	}

	return &rewritten
}

func getAuthEndpointFromURL(urlstring string) (*url.URL, error) {
	htmlDocScrapeURL, err := url.Parse(urlstring)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse URL")
	}

	if htmlDocScrapeURL.Scheme != schemeHTTPS {
		return nil, fmt.Errorf("url must be https")
	}
	if utils.IsHostnameInternal(htmlDocScrapeURL.Hostname()) {
		return nil, errors.New("unable to use provided host")
	}

	client := utils.GetFederationHTTPClient()
	r, err := client.Get(htmlDocScrapeURL.String())
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	scrapedHTMLDocument, err := html.Parse(r.Body)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse html at remote auth host")
	}
	authorizationEndpointTag := cascadia.MustCompile("link[rel=authorization_endpoint]").MatchAll(scrapedHTMLDocument)
	if len(authorizationEndpointTag) == 0 {
		// No endpoint advertised by the site: authenticate through the shared
		// provider, which is what most small IndieAuth sites rely on.
		fallback, err := url.Parse(defaultIndieAuthProvider)
		if err != nil {
			return nil, errors.Wrap(err, "unable to parse the default indieauth provider")
		}

		return fallback, nil
	}

	for _, attr := range authorizationEndpointTag[len(authorizationEndpointTag)-1].Attr {
		if attr.Key == "href" {
			u, err := url.Parse(attr.Val)
			if err != nil {
				return nil, errors.Wrap(err, "unable to parse authorization endpoint")
			}

			// If it is a relative URL we an fill in the missing components
			// by using the original URL we scraped, since it is the same host.
			if u.Scheme == "" {
				u.Scheme = htmlDocScrapeURL.Scheme
			}

			if u.Host == "" {
				u.Host = htmlDocScrapeURL.Host
			}

			return successorIndieAuthEndpoint(u), nil
		}
	}

	return nil, fmt.Errorf("unable to find href value for authorization_endpoint")
}

func createCodeChallenge(codeVerifier string) string {
	sha256hash := sha256.Sum256([]byte(codeVerifier))

	encodedHashedCode := strings.TrimRight(base64.URLEncoding.EncodeToString(sha256hash[:]), "=")

	return encodedHashedCode
}
