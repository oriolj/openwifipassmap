// Command wifispot is an offline-first CLI for the public OpenWifiPassMap directory.
//
//	wifispot sync   --lat 41.39 --lng 2.17 --radius 200   # download an area
//	wifispot nearby --lat 41.39 --lng 2.17 --radius 5      # query offline cache
//	wifispot scan                                          # match in-range SSIDs
//	wifispot connect "BlueBottle-Guest"                    # connect via cache
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/oriolj/openwifipassmap/internal/apiclient"
	"github.com/oriolj/openwifipassmap/internal/cache"
	"github.com/oriolj/openwifipassmap/internal/models"
	"github.com/oriolj/openwifipassmap/internal/wifi"
)

// defaultServer is the fallback base URL when neither --server nor
// WIFISPOT_SERVER is provided. Override at build time with
// `-ldflags "-X main.defaultServer=https://example.com"`.
var defaultServer = "http://localhost:8080"

// serverMaxRadiusKM mirrors the backend's area-radius clamp (api.areaMaxRadius).
const serverMaxRadiusKM = 300.0

// cityCoords maps a handful of well-known city aliases to their lat/lng so the
// user can `wifispot sync --city barcelona` instead of remembering coordinates.
// Keys are lowercase; the helpers below normalise user input.
var cityCoords = map[string]struct {
	Lat, Lng float64
}{
	"girona":    {41.9831, 2.8249},
	"barcelona": {41.3851, 2.1734},
	"madrid":    {40.4168, -3.7038},
	"berlin":    {52.5200, 13.4050},
	"sf":        {37.7749, -122.4194},
	"la":        {34.0522, -118.2437},
	"nyc":       {40.7128, -74.0060},
	"london":    {51.5074, -0.1278},
	"paris":     {48.8566, 2.3522},
	"lisboa":    {38.7223, -9.1393},
}

// cityList returns the supported city aliases sorted alphabetically for use in
// help/error messages.
func cityList() []string {
	names := make([]string, 0, len(cityCoords))
	for k := range cityCoords {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sync":
		err = cmdSync(os.Args[2:])
	case "import":
		err = cmdImport(os.Args[2:])
	case "nearby":
		err = cmdNearby(os.Args[2:])
	case "scan":
		err = cmdScan(os.Args[2:])
	case "connect":
		err = cmdConnect(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`wifispot — offline-first public WiFi directory CLI

Commands:
  sync     Download public spots for an area into the local cache
  import   Upload spots from a CSV to the server (requires login)
  nearby   List cached spots near a point (works offline)
  scan     Scan in-range networks and match them to the cache
  connect  Connect to a network using a cached password

Run "wifispot <command> -h" for command flags.
`)
}

func defaultCachePath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "wifispot", "cache.db")
}

func openCache(path string) (*cache.Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return cache.Open(path)
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	lat := fs.Float64("lat", 0, "center latitude (required unless --city is given)")
	lng := fs.Float64("lng", 0, "center longitude (required unless --city is given)")
	city := fs.String("city", "", "city alias instead of --lat/--lng (one of: "+strings.Join(cityList(), ", ")+")")
	radius := fs.Float64("radius", 200, "radius in km")
	server := fs.String("server", env("WIFISPOT_SERVER", defaultServer), "server base URL")
	dbPath := fs.String("db", defaultCachePath(), "local cache path")
	_ = fs.Parse(args)
	if *city != "" {
		coords, ok := cityCoords[strings.ToLower(strings.TrimSpace(*city))]
		if !ok {
			return fmt.Errorf("unknown --city %q; known: %s", *city, strings.Join(cityList(), ", "))
		}
		if !flagSet(fs, "lat") {
			*lat = coords.Lat
		}
		if !flagSet(fs, "lng") {
			*lng = coords.Lng
		}
	}
	if !flagSet(fs, "lat") && !flagSet(fs, "lng") && *city == "" {
		return fmt.Errorf("provide --city, or both --lat and --lng (known cities: %s)",
			strings.Join(cityList(), ", "))
	}
	// The server clamps area radius (areaMaxRadius = 300 km); warn so the user
	// isn't misled into thinking a larger area was cached.
	if *radius > serverMaxRadiusKM {
		fmt.Fprintf(os.Stderr, "warning: server caps area radius at %.0f km; --radius %.0f will be clamped\n",
			serverMaxRadiusKM, *radius)
	}

	c, err := openCache(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()

	ctx := context.Background()
	client := apiclient.New(*server)
	cursor := ""
	total := 0
	for {
		spots, next, err := client.AreaPage(ctx, *lat, *lng, *radius, cursor)
		if err != nil {
			return err
		}
		for _, sp := range spots {
			if err := c.Upsert(ctx, sp); err != nil {
				return err
			}
		}
		total += len(spots)
		if next == "" {
			break
		}
		cursor = next
	}
	_ = c.SetMeta(ctx, "last_lat", strconv.FormatFloat(*lat, 'f', -1, 64))
	_ = c.SetMeta(ctx, "last_lng", strconv.FormatFloat(*lng, 'f', -1, 64))
	n, _ := c.Count(ctx)
	fmt.Printf("synced %d spot(s) within %.0f km; cache now holds %d spot(s) at %s\n",
		total, min(*radius, serverMaxRadiusKM), n, *dbPath)
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	server := fs.String("server", env("WIFISPOT_SERVER", defaultServer), "server base URL")
	token := fs.String("token", env("WIFISPOT_TOKEN", ""), "bearer token (prefer the WIFISPOT_TOKEN env var)")
	username := fs.String("username", "", "account username (logs in to obtain a token)")
	// Password defaults from the env, not an argv flag, so it doesn't leak via
	// `ps`/process listing or shell history; if absent we prompt with no echo.
	password := fs.String("password", env("WIFISPOT_PASSWORD", ""), "account password (prefer WIFISPOT_PASSWORD env, or be prompted)")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: wifispot import [--server URL] [--token T | --username U] <file.csv>")
	}

	warnInsecureServer(*server)

	ctx := context.Background()
	client := apiclient.New(*server)

	tok := *token
	if tok == "" {
		if *username == "" {
			return fmt.Errorf("provide --token (or WIFISPOT_TOKEN), or --username to log in")
		}
		pw := *password
		if pw == "" {
			var err error
			if pw, err = promptPassword(); err != nil {
				return err
			}
		}
		var err error
		if tok, err = client.Login(ctx, *username, pw); err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("server returned an empty token")
		}
	}

	f, err := os.Open(rest[0])
	if err != nil {
		return err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	// Leave FieldsPerRecord at its default (0): csv enforces that every row has
	// the same column count as the header, so a misaligned row (e.g. a stray
	// comma) is reported as a parse error instead of silently shifting columns.
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("reading CSV header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, c := range []string{"essid", "lat", "lng"} {
		if _, ok := col[c]; !ok {
			return fmt.Errorf("CSV missing required column %q (need at least essid, lat, lng)", c)
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	added, skipped, failed := 0, 0, 0
	line := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			// Count a malformed row as a failure and keep going, so one bad row
			// doesn't abandon the rest of the file.
			fmt.Printf("line %d: FAILED (CSV parse error: %v)\n", line, err)
			failed++
			continue
		}
		essid, latStr, lngStr := get(rec, "essid"), get(rec, "lat"), get(rec, "lng")
		if essid == "" || latStr == "" || lngStr == "" {
			fmt.Printf("line %d: skipped (missing essid/lat/lng)\n", line)
			skipped++
			continue
		}
		lat, err1 := strconv.ParseFloat(latStr, 64)
		lng, err2 := strconv.ParseFloat(lngStr, 64)
		if err1 != nil || err2 != nil {
			fmt.Printf("line %d: skipped (invalid lat/lng %q,%q)\n", line, latStr, lngStr)
			skipped++
			continue
		}
		id, err := client.CreateSpot(ctx, tok, apiclient.SpotInput{
			VenueName: get(rec, "venue_name"),
			ESSID:     essid,
			Password:  get(rec, "password"),
			AuthType:  get(rec, "auth_type"),
			Lat:       lat,
			Lng:       lng,
			Notes:     get(rec, "notes"),
		})
		if err != nil {
			fmt.Printf("line %d: FAILED %q: %v\n", line, essid, err)
			failed++
			continue
		}
		fmt.Printf("line %d: added %q (%s)\n", line, essid, id)
		added++
	}
	fmt.Printf("done: %d added, %d skipped, %d failed\n", added, skipped, failed)
	if failed > 0 {
		return fmt.Errorf("%d row(s) failed to import", failed)
	}
	return nil
}

func cmdNearby(args []string) error {
	fs := flag.NewFlagSet("nearby", flag.ExitOnError)
	lat := fs.Float64("lat", 0, "latitude")
	lng := fs.Float64("lng", 0, "longitude")
	radius := fs.Float64("radius", 5, "radius in km")
	dbPath := fs.String("db", defaultCachePath(), "local cache path")
	_ = fs.Parse(args)

	c, err := openCache(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()

	if !flagSet(fs, "lat") || !flagSet(fs, "lng") {
		return fmt.Errorf("--lat and --lng are required (try the coordinates you last synced)")
	}

	spots, err := c.Nearby(context.Background(), *lat, *lng, *radius)
	if err != nil {
		return err
	}
	if len(spots) == 0 {
		fmt.Println("no cached spots within", *radius, "km — run `wifispot sync` first")
		return nil
	}
	printSpots(spots)
	return nil
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	dbPath := fs.String("db", defaultCachePath(), "local cache path")
	_ = fs.Parse(args)

	ssids, err := wifi.Scan()
	if err != nil {
		return err
	}
	if len(ssids) == 0 {
		fmt.Println("no networks in range")
		return nil
	}

	c, err := openCache(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()

	// Build an essid → spot lookup from a wide cache query around the world is
	// not possible offline without coords; instead match against everything by
	// pulling a generous radius from the last sync center if known.
	// First match wins, so `scan` shows the same spot's password that `connect`
	// will use (connect also takes the first ESSID match). Without this, chain
	// venues reusing one SSID would show one password but connect with another.
	known := map[string]*models.Spot{}
	for _, sp := range allCached(c) {
		if _, exists := known[sp.ESSID]; !exists {
			known[sp.ESSID] = sp
		}
	}

	// Filter the scan to spots we have cached — unknown SSIDs (and any
	// passwords the user happens to have for them) are nobody's business.
	matched := make([]*models.Spot, 0, len(ssids))
	for _, ssid := range ssids {
		if sp, ok := known[ssid]; ok {
			matched = append(matched, sp)
		}
	}
	if len(matched) == 0 {
		fmt.Printf("detected %d network(s) in range, none known\n", len(ssids))
		return nil
	}

	fmt.Printf("detected %d network(s) in range, %d known:\n", len(ssids), len(matched))
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "SSID\tPASSWORD")
	for _, sp := range matched {
		pw := sp.Password
		if pw == "" {
			pw = "(open)"
		}
		fmt.Fprintf(w, "%s\t%s\n", sp.ESSID, pw)
	}
	return w.Flush()
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	dbPath := fs.String("db", defaultCachePath(), "local cache path")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: wifispot connect <ssid>")
	}
	ssid := rest[0]

	c, err := openCache(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()

	var found *models.Spot
	for _, sp := range allCached(c) {
		if sp.ESSID == ssid {
			found = sp
			break
		}
	}
	if found == nil {
		return fmt.Errorf("no cached spot with SSID %q — run `wifispot sync` near it first", ssid)
	}
	fmt.Printf("connecting to %q…\n", ssid)
	if err := wifi.Connect(found.ESSID, found.Password); err != nil {
		return err
	}
	fmt.Println("connected")
	return nil
}

// allCached returns every spot in the cache (a very large radius around 0,0
// won't cover the globe, so we read directly via a full-planet bounding box).
func allCached(c *cache.Cache) []*models.Spot {
	spots, err := c.Nearby(context.Background(), 0, 0, 21000) // > Earth's half-circumference
	if err != nil {
		return nil
	}
	return spots
}

func printSpots(spots []*models.Spot) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DIST(km)\tVENUE\tSSID\tSECURITY\tPASSWORD")
	for _, sp := range spots {
		venue := sp.VenueName
		if venue == "" {
			venue = "-"
		}
		pw := sp.Password
		if pw == "" {
			pw = "(open)"
		}
		fmt.Fprintf(w, "%.1f\t%s\t%s\t%s\t%s\n", deref(sp.DistanceKM), venue, sp.ESSID, sp.AuthType, pw)
	}
	_ = w.Flush()
}

func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// warnInsecureServer warns when credentials would be sent over cleartext http
// to a non-local host.
func warnInsecureServer(server string) {
	u, err := url.Parse(server)
	if err != nil {
		return
	}
	host := u.Hostname()
	if u.Scheme == "http" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		fmt.Fprintf(os.Stderr, "warning: sending credentials over cleartext http to %q — use https\n", host)
	}
}

// promptPassword reads a password from the terminal without echoing it.
func promptPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(b), nil
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
