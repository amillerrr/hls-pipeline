package cdn

import (
	"net/url"
	"regexp"
	"strings"
)

// ManifestRewriter rewrites manifest URLs for CDN routing.
type ManifestRewriter struct {
	originalBaseURL string
}

// NewManifestRewriter creates a new manifest rewriter.
func NewManifestRewriter(originalBaseURL string) *ManifestRewriter {
	return &ManifestRewriter{
		originalBaseURL: strings.TrimSuffix(originalBaseURL, "/"),
	}
}

// RewriteHLSMaster rewrites an HLS master playlist for a specific CDN.
func (r *ManifestRewriter) RewriteHLSMaster(manifest string, cdn *Provider, sessionID string) string {
	lines := strings.Split(manifest, "\n")
	var result strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			// Handle URI attributes in tags
			line = r.rewriteURIAttribute(line, cdn, sessionID)
			result.WriteString(line + "\n")
		} else if strings.TrimSpace(line) != "" {
			// This is a URL line
			rewritten := r.rewriteURL(line, cdn, sessionID)
			result.WriteString(rewritten + "\n")
		} else {
			result.WriteString(line + "\n")
		}
	}

	return result.String()
}

// RewriteHLSVariant rewrites an HLS variant playlist for a specific CDN.
func (r *ManifestRewriter) RewriteHLSVariant(manifest string, cdn *Provider, sessionID string) string {
	lines := strings.Split(manifest, "\n")
	var result strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			// Rewrite init segment URI
			line = r.rewriteURIAttribute(line, cdn, sessionID)
			result.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "#EXT-X-PART:") {
			// Rewrite partial segment URI
			line = r.rewriteURIAttribute(line, cdn, sessionID)
			result.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "#EXT-X-PRELOAD-HINT:") {
			// Rewrite preload hint URI
			line = r.rewriteURIAttribute(line, cdn, sessionID)
			result.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "#EXT-X-KEY:") {
			// Rewrite key URI (but preserve DRM URIs)
			line = r.rewriteKeyURI(line, cdn, sessionID)
			result.WriteString(line + "\n")
		} else if strings.HasPrefix(line, "#") {
			result.WriteString(line + "\n")
		} else if strings.TrimSpace(line) != "" {
			// This is a segment URL line
			rewritten := r.rewriteURL(line, cdn, sessionID)
			result.WriteString(rewritten + "\n")
		} else {
			result.WriteString(line + "\n")
		}
	}

	return result.String()
}

// RewriteDASHManifest rewrites a DASH manifest for a specific CDN.
func (r *ManifestRewriter) RewriteDASHManifest(manifest string, cdn *Provider, sessionID string) string {
	// Rewrite BaseURL elements
	baseURLRegex := regexp.MustCompile(`(<BaseURL>)([^<]+)(</BaseURL>)`)
	manifest = baseURLRegex.ReplaceAllStringFunc(manifest, func(match string) string {
		parts := baseURLRegex.FindStringSubmatch(match)
		if len(parts) == 4 {
			rewritten := r.rewriteURL(parts[2], cdn, sessionID)
			return parts[1] + rewritten + parts[3]
		}
		return match
	})

	// Rewrite initialization and media attributes in SegmentTemplate
	initRegex := regexp.MustCompile(`(initialization=")([^"]+)(")`)
	manifest = initRegex.ReplaceAllStringFunc(manifest, func(match string) string {
		parts := initRegex.FindStringSubmatch(match)
		if len(parts) == 4 {
			rewritten := r.rewriteURL(parts[2], cdn, sessionID)
			return parts[1] + rewritten + parts[3]
		}
		return match
	})

	mediaRegex := regexp.MustCompile(`(media=")([^"]+)(")`)
	manifest = mediaRegex.ReplaceAllStringFunc(manifest, func(match string) string {
		parts := mediaRegex.FindStringSubmatch(match)
		if len(parts) == 4 {
			rewritten := r.rewriteURL(parts[2], cdn, sessionID)
			return parts[1] + rewritten + parts[3]
		}
		return match
	})

	return manifest
}

// rewriteURL rewrites a URL to use the specified CDN.
func (r *ManifestRewriter) rewriteURL(originalURL string, cdn *Provider, sessionID string) string {
	// Skip if already an absolute URL to a different domain
	if strings.HasPrefix(originalURL, "http://") || strings.HasPrefix(originalURL, "https://") {
		parsed, err := url.Parse(originalURL)
		if err != nil {
			return originalURL
		}

		// Replace the host with CDN host
		cdnURL, err := url.Parse(cdn.BaseURL)
		if err != nil {
			return originalURL
		}

		parsed.Scheme = cdnURL.Scheme
		parsed.Host = cdnURL.Host

		// Add session ID as query parameter if provided
		if sessionID != "" {
			q := parsed.Query()
			q.Set("_sid", sessionID)
			parsed.RawQuery = q.Encode()
		}

		return parsed.String()
	}

	// Relative URL - prepend CDN base URL
	cdnBase := strings.TrimSuffix(cdn.BaseURL, "/")
	relPath := originalURL
	if !strings.HasPrefix(relPath, "/") {
		relPath = "/" + relPath
	}

	result := cdnBase + relPath

	// Add session ID as query parameter if provided
	if sessionID != "" {
		if strings.Contains(result, "?") {
			result += "&_sid=" + sessionID
		} else {
			result += "?_sid=" + sessionID
		}
	}

	return result
}

// rewriteURIAttribute rewrites URI attributes within HLS tags.
func (r *ManifestRewriter) rewriteURIAttribute(line string, cdn *Provider, sessionID string) string {
	// Match URI="..." or URI=...
	uriRegex := regexp.MustCompile(`(URI=")([^"]+)(")`)
	return uriRegex.ReplaceAllStringFunc(line, func(match string) string {
		parts := uriRegex.FindStringSubmatch(match)
		if len(parts) == 4 {
			rewritten := r.rewriteURL(parts[2], cdn, sessionID)
			return parts[1] + rewritten + parts[3]
		}
		return match
	})
}

// rewriteKeyURI rewrites the URI in EXT-X-KEY tags.
// This preserves external DRM license server URLs.
func (r *ManifestRewriter) rewriteKeyURI(line string, cdn *Provider, sessionID string) string {
	// Extract the URI
	uriRegex := regexp.MustCompile(`URI="([^"]+)"`)
	matches := uriRegex.FindStringSubmatch(line)
	if len(matches) < 2 {
		return line
	}

	uri := matches[1]

	// Don't rewrite external DRM license URLs
	// (they typically contain specific domains like "license.widevine.com")
	if strings.Contains(uri, "license") ||
		strings.Contains(uri, "drm") ||
		strings.Contains(uri, "widevine") ||
		strings.Contains(uri, "fairplay") ||
		strings.Contains(uri, "playready") {
		return line
	}

	// Rewrite the key URL
	rewritten := r.rewriteURL(uri, cdn, sessionID)
	return uriRegex.ReplaceAllString(line, `URI="`+rewritten+`"`)
}

// AddCDNHeaders returns headers to add for CDN requests.
func AddCDNHeaders(cdn *Provider, sessionID string) map[string]string {
	headers := make(map[string]string)

	// Add session tracking header
	if sessionID != "" {
		headers["X-Session-ID"] = sessionID
	}

	// Add CDN identification
	headers["X-CDN-Provider"] = cdn.Name

	return headers
}

// StripCDNParams removes CDN-specific parameters from a URL.
func StripCDNParams(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	q := parsed.Query()
	q.Del("_sid")
	q.Del("_cdn")
	parsed.RawQuery = q.Encode()

	return parsed.String()
}
