// This package utilizes the MaxMind GeoLite2 GeoIP database https://dev.maxmind.com/geoip/geoip2/geolite2/.
// You must provide your own copy of this database for it to work.
// Read more about how this works at http://owncast.online/docs/geoip
//
// Lookups are performed entirely offline against operator-supplied .mmdb files;
// viewer IP addresses are never sent to a third-party API. City-level results
// are approximate by nature (mobile carriers, VPNs and CGNAT are often wrong)
// and must never be used for access control.
//
// Database discovery (dual-database support):
//
//   - the primary database is data/GeoLite2-City.mmdb (a MaxMind City database,
//     or any City/Country mmdb such as a DB-IP city lite build);
//   - a secondary database is auto-detected from data/dbip-city-lite.mmdb and
//     data/DBIP-City-Lite.mmdb and fills city/region/timezone values the primary
//     database does not have;
//   - a single database (either role) keeps working on its own.
//
// NewClientWithDatabasePath(s) exist as additive seams for a future
// admin-configurable path; existing callers keep the defaults.

package geoip

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/geoip2-golang"
	log "github.com/sirupsen/logrus"
)

// unknownRegionName is the placeholder for records without a subdivision name.
const unknownRegionName = "Unknown"

// Database roles reported in the admin status panel.
const (
	// DatabaseRolePrimary is the database consulted first for every lookup.
	DatabaseRolePrimary = "primary"
	// DatabaseRoleSecondary is the fill-in database for missing city data.
	DatabaseRoleSecondary = "secondary"
)

// DefaultDatabasePath is the default operator-supplied MaxMind database path.
const DefaultDatabasePath = "data/GeoLite2-City.mmdb"

// DefaultSecondaryDatabasePaths are the auto-detected fallback databases. The
// first readable file is used to fill missing city/region/timezone values. Both
// the lower-case DB-IP download name and the capitalized variant are accepted.
var DefaultSecondaryDatabasePaths = []string{
	"data/dbip-city-lite.mmdb",
	"data/DBIP-City-Lite.mmdb",
}

// DatabaseSource describes one loaded (or attempted) database file.
type DatabaseSource struct {
	Role         string `json:"role"`
	Configured   bool   `json:"configured"`
	Path         string `json:"path,omitempty"`
	DatabaseType string `json:"databaseType,omitempty"`
	BuildDate    string `json:"buildDate,omitempty"`
	HasCity      bool   `json:"hasCity"`
	HasCountry   bool   `json:"hasCountry"`
	Error        string `json:"error,omitempty"`
}

// DatabaseMetadata describes the loaded (or missing) GeoIP databases.
//
// The top-level Configured/Path/DatabaseType/BuildDate/HasCity/HasCountry/Error
// fields keep their pre-dual-database meaning (the effective primary database)
// so existing API consumers are unaffected. Secondary and Sources are additive.
type DatabaseMetadata struct {
	// Configured reports whether a usable database combination was loaded.
	Configured bool `json:"configured"`
	// Path is the primary database path.
	Path string `json:"path,omitempty"`
	// DatabaseType is the primary MaxMind database type (e.g. "GeoLite2-City").
	DatabaseType string `json:"databaseType,omitempty"`
	// BuildDate is the primary database build date in YYYY-MM-DD form.
	BuildDate string `json:"buildDate,omitempty"`
	// HasCity reports whether the loaded combination can return city data.
	HasCity bool `json:"hasCity"`
	// HasCountry reports whether the loaded combination can return a country.
	HasCountry bool `json:"hasCountry"`
	// Error is a safe, IP-free reason no database is configured.
	Error string `json:"error,omitempty"`
	// Secondary describes the fallback database, when one is loaded. Additive.
	Secondary *DatabaseSource `json:"secondary,omitempty"`
	// Sources lists every database the client loaded or attempted, primary
	// first. Additive.
	Sources []DatabaseSource `json:"sources,omitempty"`
}

// AvailableFields lists the location fields the loaded database combination can
// provide, for the admin UI documentation. It is empty when nothing is loaded.
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

// database is one opened mmdb reader plus its reported source metadata.
type database struct {
	path   string
	reader *geoip2.Reader
	source DatabaseSource
}

// Client can look up geography information for IP addresses. Lookups use the
// primary database and optionally enrich missing city/region/timezone values
// from a secondary database.
type Client struct {
	cache          sync.Map
	enabled        int32
	primaryPath    string
	secondaryPaths []string

	// readerMu guards the lazy database open and the cached readers/metadata.
	readerMu  sync.Mutex
	primary   *database
	secondary *database
	metadata  *DatabaseMetadata
}

// NewClient creates a new Client using the default operator-supplied paths and
// the auto-detected secondary databases.
func NewClient() *Client {
	return NewClientWithDatabasePaths(DefaultDatabasePath, DefaultSecondaryDatabasePaths)
}

// NewClientWithDatabasePath creates a Client with a single explicit primary
// database path and no secondary auto-detection. An empty path means the
// default path. Kept for callers/tests that want exactly one database.
func NewClientWithDatabasePath(path string) *Client {
	return NewClientWithDatabasePaths(path, nil)
}

// NewClientWithDatabasePaths creates a Client with an explicit primary path and
// secondary candidate paths. Empty primary means the default path; a nil
// secondary list disables secondary enrichment.
func NewClientWithDatabasePaths(primaryPath string, secondaryPaths []string) *Client {
	if primaryPath == "" {
		primaryPath = DefaultDatabasePath
	}
	copied := make([]string, 0, len(secondaryPaths))
	for _, path := range secondaryPaths {
		if path != "" && path != primaryPath {
			copied = append(copied, path)
		}
	}
	return &Client{
		enabled:        1, // Try to use GeoIP support by default.
		primaryPath:    primaryPath,
		secondaryPaths: copied,
	}
}

// Close releases every open database. Safe to call multiple times.
func (c *Client) Close() error {
	c.readerMu.Lock()
	defer c.readerMu.Unlock()

	var firstError error
	if c.primary != nil {
		if err := c.primary.reader.Close(); err != nil && firstError == nil {
			firstError = err
		}
		c.primary = nil
	}
	if c.secondary != nil {
		if err := c.secondary.reader.Close(); err != nil && firstError == nil {
			firstError = err
		}
		c.secondary = nil
	}
	return firstError
}

// GeoDetails stores details about a location. CountryCode, RegionName and
// TimeZone are unchanged from previous releases; City is additive and empty
// when no loaded database provides city data.
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

// fetchGeoForIP performs an offline lookup against the loaded databases. The
// primary database supplies country and whatever city data it has; missing
// city/region/timezone values are filled from the secondary database when one
// is loaded.
func (c *Client) fetchGeoForIP(ip string) *GeoDetails {
	// If GeoIP has been disabled then don't try to access it.
	if atomic.LoadInt32(&c.enabled) == 0 {
		return nil
	}
	c.openDatabases()

	ipObject := net.ParseIP(ip)
	if ipObject == nil {
		return nil
	}

	var response *GeoDetails
	if c.primary != nil {
		response = lookupDatabase(c.primary, ipObject)
	}

	switch {
	case c.primary == nil && c.secondary != nil:
		// No primary database at all: the secondary acts as the effective
		// primary so a single DB-IP file keeps working on its own.
		response = lookupDatabase(c.secondary, ipObject)
	case response != nil && c.secondary != nil:
		if enrichment := lookupDatabase(c.secondary, ipObject); enrichment != nil {
			response = mergeGeoDetails(response, enrichment)
		}
	}

	c.cache.Store(ip, response)

	return response
}

// lookupDatabase resolves one IP against one database. Country-only databases
// use the Country reader so historical behavior is preserved.
func lookupDatabase(database *database, ip net.IP) *GeoDetails {
	if database == nil || database.reader == nil {
		return nil
	}
	if database.source.HasCity {
		record, err := database.reader.City(ip)
		if err == nil {
			return geoDetailsFromCityRecord(record)
		}
		log.Debugln("geoip city lookup failed:", err)
		return nil
	}
	record, err := database.reader.Country(ip)
	if err == nil {
		return geoDetailsFromCountryRecord(record)
	}
	log.Debugln("geoip country lookup failed:", err)
	return nil
}

// mergeGeoDetails fills missing city/region/timezone values from enrichment.
// Values already present in the primary database always win.
func mergeGeoDetails(primary, enrichment *GeoDetails) *GeoDetails {
	if primary == nil || enrichment == nil {
		return primary
	}
	merged := *primary
	if merged.City == "" {
		merged.City = enrichment.City
	}
	if merged.RegionName == "" || merged.RegionName == unknownRegionName {
		if enrichment.RegionName != "" && enrichment.RegionName != unknownRegionName {
			merged.RegionName = enrichment.RegionName
		}
	}
	if merged.TimeZone == "" {
		merged.TimeZone = enrichment.TimeZone
	}
	if merged.CountryCode == "" {
		merged.CountryCode = enrichment.CountryCode
	}
	return &merged
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

	regionName := unknownRegionName
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
		RegionName:  unknownRegionName,
		TimeZone:    "",
	}
}

// openDatabases lazily opens the primary and secondary databases once and
// caches them plus their metadata. Missing secondaries are skipped silently; a
// missing/invalid primary disables lookups unless a secondary can take over.
func (c *Client) openDatabases() {
	c.readerMu.Lock()
	defer c.readerMu.Unlock()

	if c.metadata != nil {
		return
	}

	sources := []DatabaseSource{}
	primary, primarySource := openDatabaseSource(DatabaseRolePrimary, c.primaryPath, true)
	if primarySource != nil {
		sources = append(sources, *primarySource)
	}
	c.primary = primary

	for _, path := range c.secondaryPaths {
		secondary, secondarySource := openDatabaseSource(DatabaseRoleSecondary, path, false)
		if secondarySource == nil {
			continue
		}
		sources = append(sources, *secondarySource)
		if secondary != nil && c.secondary == nil {
			c.secondary = secondary
		}
	}

	c.metadata = buildDatabaseMetadata(c.primaryPath, c.primary, c.secondary, sources)
	if !c.metadata.Configured {
		log.Traceln("GeoIP support is disabled. visit https://owncast.online/docs/geoip to learn how to enable")
	}
}

// buildDatabaseMetadata summarizes the loaded databases. The top-level fields
// describe the effective primary database; Secondary/Sources are additive.
func buildDatabaseMetadata(primaryPath string, primary, secondary *database, sources []DatabaseSource) *DatabaseMetadata {
	metadata := &DatabaseMetadata{Sources: sources}
	effective := primary
	if effective == nil {
		effective = secondary
	}

	if effective == nil {
		metadata.Configured = false
		metadata.Path = primaryPath
		metadata.Error = "unable to open a GeoIP database"
		for _, source := range sources {
			if source.Role == DatabaseRolePrimary && source.Error != "" {
				metadata.Error = source.Error
				break
			}
		}
		return metadata
	}

	metadata.Configured = true
	metadata.Path = effective.path
	metadata.DatabaseType = effective.source.DatabaseType
	metadata.BuildDate = effective.source.BuildDate
	metadata.HasCity = effective.source.HasCity
	metadata.HasCountry = effective.source.HasCountry
	if secondary != nil && primary != nil {
		secondarySource := secondary.source
		metadata.Secondary = &secondarySource
	}
	if secondary != nil && secondary.source.HasCity {
		metadata.HasCity = true
	}
	if secondary != nil && secondary.source.HasCountry {
		metadata.HasCountry = true
	}
	return metadata
}

// openDatabaseSource opens one database file and builds its source metadata.
// reportMissing controls whether an absent file produces a source entry (the
// primary should be reported; optional secondaries are skipped when absent).
func openDatabaseSource(role, path string, reportMissing bool) (*database, *DatabaseSource) {
	source := DatabaseSource{Role: role, Path: path}
	if _, err := os.Stat(path); err != nil {
		if !reportMissing {
			return nil, nil
		}
		source.Configured = false
		source.Error = "database file not found"
		return nil, &source
	}

	reader, err := geoip2.Open(path)
	if err != nil {
		source.Configured = false
		source.Error = "unable to open the GeoIP database"
		if reportMissing {
			return nil, &source
		}
		return nil, &source
	}

	readerMetadata := reader.Metadata()
	databaseType := readerMetadata.DatabaseType
	source.Configured = true
	source.DatabaseType = databaseType
	source.BuildDate = time.Unix(int64(readerMetadata.BuildEpoch), 0).UTC().Format("2006-01-02") //nolint:gosec
	source.HasCity = strings.Contains(strings.ToLower(databaseType), "city")
	source.HasCountry = true

	return &database{path: path, reader: reader, source: source}, &source
}

// DatabaseMetadata returns the discovered database metadata, opening the
// databases on first call. This is the accessor the admin Live Stream Status
// provider uses; it is also safe to call at startup to log the loaded sources.
func (c *Client) DatabaseMetadata() DatabaseMetadata {
	c.openDatabases()

	c.readerMu.Lock()
	defer c.readerMu.Unlock()
	if c.metadata == nil {
		return DatabaseMetadata{Path: c.primaryPath}
	}
	return *c.metadata
}

// HasCityDatabase reports whether the loaded combination provides city data.
func (c *Client) HasCityDatabase() bool {
	return c.DatabaseMetadata().HasCity
}

// HasSecondaryDatabase reports whether a secondary database is loaded.
func (c *Client) HasSecondaryDatabase() bool {
	return c.DatabaseMetadata().Secondary != nil
}

// AvailableFields lists the location fields the loaded combination provides.
func (c *Client) AvailableFields() []string {
	return c.DatabaseMetadata().AvailableFields()
}

// DatabasePath returns the configured primary database path.
func (c *Client) DatabasePath() string {
	return c.primaryPath
}

// SecondaryDatabasePath returns the loaded secondary database path, if any.
func (c *Client) SecondaryDatabasePath() string {
	metadata := c.DatabaseMetadata()
	if metadata.Secondary == nil {
		return ""
	}
	return metadata.Secondary.Path
}

// String implements fmt.Stringer for safe logging: paths, types and build dates
// only - never viewer IP addresses.
func (c *Client) String() string {
	metadata := c.DatabaseMetadata()
	if metadata.Secondary != nil {
		return fmt.Sprintf("geoip database %s (type=%s build=%s) + secondary %s (type=%s build=%s)",
			c.primaryPath, metadata.DatabaseType, metadata.BuildDate,
			metadata.Secondary.Path, metadata.Secondary.DatabaseType, metadata.Secondary.BuildDate)
	}
	return fmt.Sprintf("geoip database %s (type=%s build=%s)", c.primaryPath, metadata.DatabaseType, metadata.BuildDate)
}
