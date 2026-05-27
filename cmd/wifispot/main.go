// Command wifispot is an offline-first CLI for the public WiFi Spots directory.
//
//	wifispot sync   --lat 41.39 --lng 2.17 --radius 200   # download an area
//	wifispot nearby --lat 41.39 --lng 2.17 --radius 5      # query offline cache
//	wifispot scan                                          # match in-range SSIDs
//	wifispot connect "BlueBottle-Guest"                    # connect via cache
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/oriolj/wifi_psw_sharer/internal/apiclient"
	"github.com/oriolj/wifi_psw_sharer/internal/cache"
	"github.com/oriolj/wifi_psw_sharer/internal/models"
	"github.com/oriolj/wifi_psw_sharer/internal/wifi"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sync":
		err = cmdSync(os.Args[2:])
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
	lat := fs.Float64("lat", 0, "center latitude (required)")
	lng := fs.Float64("lng", 0, "center longitude (required)")
	radius := fs.Float64("radius", 200, "radius in km")
	server := fs.String("server", env("WIFISPOT_SERVER", "http://localhost:8080"), "server base URL")
	dbPath := fs.String("db", defaultCachePath(), "local cache path")
	_ = fs.Parse(args)
	if !flagSet(fs, "lat") || !flagSet(fs, "lng") {
		return fmt.Errorf("--lat and --lng are required")
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
		total, *radius, n, *dbPath)
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
	known := map[string]*models.Spot{}
	for _, sp := range allCached(c) {
		known[sp.ESSID] = sp
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "IN RANGE\tKNOWN?\tPASSWORD")
	for _, ssid := range ssids {
		if sp, ok := known[ssid]; ok {
			pw := sp.Password
			if pw == "" {
				pw = "(open)"
			}
			fmt.Fprintf(w, "%s\t✓\t%s\n", ssid, pw)
		} else {
			fmt.Fprintf(w, "%s\t-\t\n", ssid)
		}
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

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
