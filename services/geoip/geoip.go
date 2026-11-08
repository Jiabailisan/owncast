// This package utilizes the MaxMind GeoLite2 GeoIP database https://dev.maxmind.com/geoip/geoip2/geolite2/.
// You must provide your own copy of this database for it to work.
// Read more about how this works at http://owncast.online/docs/geoip
//
// Lookups are performed entirely offline against the operator-supplied .mmdb
// file; viewer IP addresses are never sent to a third-party API. City-level
// results are approximate by nature (mobile carriers, VPNs and CGNAT are often
// wrong) and must never be used for access control.
//
// Database path: the documented operator mechanism is to place the database at
// data/GeoLite2-City.mmdb. NewClientWithDatabasePath exists as an additive seam
// for a future admin-configurable path; existing callers keep the default.

package geoip

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/geoip2-golang"
	log "github.com/sirupsen/logrus"
)

// DefaultDatabasePath is the default operator-supplied MaxMind database path.
const DefaultDatabasePath = "data/GeoLite2-City.mmdb"

// DatabaseMetadata describes the loaded (or missing) GeoIP database.
//
// It is intentionally independent of any admin/API model so the geoip package
// keeps its current dependency direction (models imports geoip, not the other
// way around).
type DatabaseMetadata struct {
	// Configured reports whether a readable database was loaded.
	Configured bool `json:"configured"`
	// Path is the operator-supplied database path.
	Path string `json:"path,omitempty"`
	// DatabaseType is the MaxMind database type (e.g. "GeoLite2-City").
	DatabaseType string `json:"databaseType,omitempty"`
	// BuildDate is the database build date in YYYY-MM-DD form.
	BuildDate string `json:"buildDate,omitempty"`
	// HasCity reports whether the database can return city-level data.
	HasCity bool `json:"hasCity"`
	// HasCountry reports whether the database can return a country.
	HasCountry bool `json:"hasCountry"`
	// Error is a safe, IP-free reason the database is not configured.
	Error string `json:"error,omitempty"`
}

// AvailableFields lists the location fields this database can provide, for the
// admin UI documentation. It is empty when no database is configured.
func (m DatabaseMetadata) AvailableFields() []string {
	if !m.Configured {
		return []string{}
	}
	fields := make([]string, 0, 4)
	if m.HasCountry {
		fields = append(fields, "country")
	}
	if m.HasCity {
		fields = append(fields, "region", "city", "timezone")
	}
	return fields
}

// Client can look up geography information for IP addresses.
type Client struct {
	cache        sync.Map
	enabled      int32
	databasePath string

	// readerMu guards the lazy database open and the cached reader/metadata.
	readerMu sync.Mutex
	reader   *geoip2.Reader
	metadata *DatabaseMetadata
}

// NewClient creates a new Client using the default operator-supplied path.
func NewClient() *Client {
	return NewClientWithDatabasePath(DefaultDatabasePath)
}

// NewClientWithDatabasePath creates a Client for a specific database path. An
// empty path means the default path. It is additive: existing callers can keep
// using NewClient.
func NewClientWithDatabasePath(path string) *Client {
	if path == "" {
		path = DefaultDatabasePath
	}
	return &Client{
		enabled:      1, // Try to use GeoIP support by default.
		databasePath: path,
	}
}

// Close releases the open database, if any. Safe to call multiple times.
func (c *Client) Close() error {
	c.readerMu.Lock()
	defer c.readerMu.Unlock()

	if c.reader == nil {
		return nil
	}
	err := c.reader.Close()
	c.reader = nil
	return err
}

// GeoDetails stores details about a location. CountryCode, RegionName and
// TimeZone are unchanged from previous releases; City is additive and empty
// when the configured database is country-only.
type GeoDetails struct {
	CountryCode string `json:"countryCode"`
	RegionName  string `json:"regionName"`
	TimeZone    string `json:"timeZone"`
	City        string `json:"city,omitempty"`
}

// GetGeoFromIP returns geo details associated with an IP address if we
// have previously fetched it.
func (c *Client) GetGeoFromIP(ip string) *GeoDetails {
	if cachedGeoDetails, ok := c.cache.Load(ip); ok {
		return cachedGeoDetails.(*GeoDetails)
	}

	if ip == "::1" || ip == "127.0.0.1" {
		return &GeoDetails{
			CountryCode: "N/A",
			RegionName:  "Localhost",
			TimeZone:    "",
		}
	}

	return c.fetchGeoForIP(ip)
}

// fetchGeoForIP performs an offline lookup against the supplied database.
func (c *Client) fetchGeoForIP(ip string) *GeoDetails {
	// If GeoIP has been disabled then don't try to access it.
	if atomic.LoadInt32(&c.enabled) == 0 {
		return nil
	}

	database := c.openDatabase()
	if database == nil {
		return nil
	}

	ipObject := net.ParseIP(ip)
	if ipObject == nil {
		return nil
	}

	var response *GeoDetails
	if c.HasCityDatabase() {
		record, err := database.City(ipObject)
		if err == nil {
			response = geoDetailsFromCityRecord(record)
		} else {
			log.Warnln("geoip city lookup failed:", err)
		}
	} else {
		record, err := database.Country(ipObject)
		if err == nil {
			response = geoDetailsFromCountryRecord(record)
		} else {
			log.Warnln("geoip country lookup failed:", err)
		}
	}

	c.cache.Store(ip, response)

	return response
}

// geoDetailsFromCityRecord maps a MaxMind City record to the Owncast shape.
// It filters records without a country and anonymous proxies, exactly like the
// pre-extension behavior, and adds the city when the database has one.
func geoDetailsFromCityRecord(record *geoip2.City) *GeoDetails {
	// If no country is available then exit.
	// If we believe this IP to be anonymous then no reason to report it.
	if record.Country.IsoCode == "" || record.Traits.IsAnonymousProxy {
		return nil
	}

	regionName := "Unknown"
	if len(record.Subdivisions) > 0 {
		if region, ok := record.Subdivisions[0].Names["en"]; ok && region != "" {
			regionName = region
		}
	}

	return &GeoDetails{
		CountryCode: record.Country.IsoCode,
		RegionName:  regionName,
		TimeZone:    record.Location.TimeZone,
		City:        record.City.Names["en"],
	}
}

// geoDetailsFromCountryRecord maps a MaxMind Country record to the Owncast
// shape for country-only databases (no city/region data available).
func geoDetailsFromCountryRecord(record *geoip2.Country) *GeoDetails {
	if record.Country.IsoCode == "" || record.Traits.IsAnonymousProxy {
		return nil
	}
	return &GeoDetails{
		CountryCode: record.Country.IsoCode,
		RegionName:  "Unknown",
		TimeZone:    "",
	}
}

// openDatabase lazily opens the database once and caches the reader plus its
// metadata. A missing/invalid database disables lookups permanently and is
// reported through DatabaseMetadata rather than an error per lookup.
func (c *Client) openDatabase() *geoip2.Reader {
	c.readerMu.Lock()
	defer c.readerMu.Unlock()

	if c.reader != nil {
		return c.reader
	}
	// A previous open attempt failed: keep returning nil without retrying on
	// every viewer request.
	if c.metadata != nil && !c.metadata.Configured {
		return nil
	}

	reader, err := geoip2.Open(c.databasePath)
	if err != nil {
		metadata := DatabaseMetadata{
			Configured: false,
			Path:       c.databasePath,
			Error:      "unable to open the configured GeoIP database",
		}
		c.metadata = &metadata
		atomic.StoreInt32(&c.enabled, 0)
		log.Traceln("GeoIP support is disabled. visit https://owncast.online/docs/geoip to learn how to enable:", err)
		return nil
	}

	readerMetadata := reader.Metadata()
	databaseType := readerMetadata.DatabaseType
	metadata := DatabaseMetadata{
		Configured:   true,
		Path:         c.databasePath,
		DatabaseType: databaseType,
		BuildDate:    time.Unix(int64(readerMetadata.BuildEpoch), 0).UTC().Format("2006-01-02"), //nolint:gosec // Unix seconds always fit in int64.
		HasCity:      strings.Contains(strings.ToLower(databaseType), "city"),
		HasCountry:   true,
	}
	c.reader = reader
	c.metadata = &metadata
	return reader
}

// DatabaseMetadata returns the discovered database metadata, opening the
// database on first call. This is the accessor the admin Live Stream Status
// provider uses; it is also safe to call at startup to log the loaded type and
// build date.
func (c *Client) DatabaseMetadata() DatabaseMetadata {
	_ = c.openDatabase()

	c.readerMu.Lock()
	defer c.readerMu.Unlock()
	if c.metadata == nil {
		return DatabaseMetadata{Path: c.databasePath}
	}
	return *c.metadata
}

// HasCityDatabase reports whether the configured database provides city data.
func (c *Client) HasCityDatabase() bool {
	return c.DatabaseMetadata().HasCity
}

// AvailableFields lists the location fields the configured database provides.
func (c *Client) AvailableFields() []string {
	return c.DatabaseMetadata().AvailableFields()
}

// DatabasePath returns the configured database path (for status/log lines).
func (c *Client) DatabasePath() string {
	return c.databasePath
}

// String implements fmt.Stringer for safe logging: path, type and build date
// only - never viewer IP addresses.
func (c *Client) String() string {
	metadata := c.DatabaseMetadata()
	return fmt.Sprintf("geoip database %s (type=%s build=%s)", c.databasePath, metadata.DatabaseType, metadata.BuildDate)
}
