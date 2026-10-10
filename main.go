// qdrantcli – Qdrant-Abfragen mit dem eigenen Token des Mitarbeiters.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

// ---------- Konfiguration ----------

type Config struct {
	URL         string `json:"url"`
	Token       string `json:"token,omitempty"`
	EmbedURL    string `json:"embed_url,omitempty"`
	EmbedModel  string `json:"embed_model,omitempty"`
	ServerModel string `json:"server_model,omitempty"`
}

func configPath() (string, error) {
	if p := os.Getenv("QDRANTCLI_CONFIG"); p != "" {
		return p, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "qdrantcli", "config.json"), nil
}

func loadConfig() Config {
	var c Config
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	if v := os.Getenv("QDRANTCLI_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("QDRANTCLI_TOKEN"); v != "" {
		c.Token = v
	}
	if v := os.Getenv("QDRANTCLI_EMBED_URL"); v != "" {
		c.EmbedURL = v
	}
	if v := os.Getenv("QDRANTCLI_EMBED_MODEL"); v != "" {
		c.EmbedModel = v
	}
	if v := os.Getenv("QDRANTCLI_SERVER_MODEL"); v != "" {
		c.ServerModel = v
	}
	return c
}

func saveConfig(c Config) (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(p, 0o600)
	return p, nil
}

// ---------- HTTP ----------

type client struct {
	base  string
	token string
	http  *http.Client
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func newClient(c Config) (*client, error) {
	if c.URL == "" {
		return nil, errors.New("keine URL konfiguriert – erst `qdrantcli login --url <url>` oder QDRANTCLI_URL setzen")
	}
	return &client{base: strings.TrimRight(c.URL, "/"), token: c.Token, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (cl *client) do(method, path, collection string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, cl.base+path, rd)
	if err != nil {
		return err
	}
	if cl.token != "" {
		req.Header.Set("api-key", cl.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.http.Do(req)
	if err != nil {
		return fmt.Errorf("keine Verbindung zu %s: %v", cl.base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return httpErr(resp.StatusCode, collection, data)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("ungültige Antwort vom Server: %v", err)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func httpErr(code int, collection string, data []byte) error {
	detail := ""
	var e struct {
		Status struct {
			Error string `json:"error"`
		} `json:"status"`
	}
	if json.Unmarshal(data, &e) == nil && e.Status.Error != "" {
		detail = e.Status.Error
	} else {
		detail = strings.TrimSpace(string(data))
		if len(detail) > 200 {
			detail = detail[:200]
		}
	}
	switch code {
	case 401:
		return &apiError{code, "Token ungültig oder fehlt (401) – `qdrantcli login` erneut ausführen"}
	case 403:
		if collection != "" {
			return &apiError{code, fmt.Sprintf("Kein Zugriff mit deinem Token auf %s", collection)}
		}
		return &apiError{code, "Kein Zugriff mit deinem Token (403)"}
	case 404:
		if collection != "" {
			return &apiError{code, fmt.Sprintf("Collection %s nicht gefunden (404): %s", collection, detail)}
		}
	}
	return &apiError{code, fmt.Sprintf("Serverfehler %d: %s", code, detail)}
}

// ---------- Hilfen ----------

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func die(err error) {
	fmt.Fprintln(os.Stderr, "Fehler:", err)
	os.Exit(1)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func newFS(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(usage))
		fmt.Fprintln(os.Stderr, "\nOptionen:")
		fs.PrintDefaults()
	}
	return fs
}

// parseArgs erlaubt Flags vor und nach Positionsargumenten.
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if rest[0] == "--" {
			pos = append(pos, rest[1:]...)
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return pos
}

func buildFilter(filters []string) (map[string]any, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	var must []any
	for _, f := range filters {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("ungültiger Filter %q – Format key=value", f)
		}
		var val any = v
		if v == "true" || v == "false" {
			val = v == "true"
		} else if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			val = i
		}
		must = append(must, map[string]any{"key": k, "match": map[string]any{"value": val}})
	}
	return map[string]any{"must": must}, nil
}

func pickFields(p map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return p
	}
	o := map[string]any{}
	for _, f := range fields {
		if v, ok := p[f]; ok {
			o[f] = v
		}
	}
	return o
}

func splitFields(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// readSecret liest ohne Echo, falls stdin ein Terminal ist (Unix via termios-ioctl).
func readSecret(prompt string) (string, error) {
	fi, _ := os.Stdin.Stat()
	isTTY := fi != nil && fi.Mode()&os.ModeCharDevice != 0
	if isTTY {
		fmt.Fprint(os.Stderr, prompt)
		restore := disableEcho()
		defer func() { restore(); fmt.Fprintln(os.Stderr) }()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("kein Token gelesen")
	}
	return strings.TrimSpace(line), nil
}

// ---------- Befehle ----------

const rootUsage = `qdrantcli – Qdrant der Firma mit deinem eigenen Token abfragen

Nutzung:
  qdrantcli <befehl> [optionen]

Befehle:
  login        URL + Token speichern
  whoami       Verbindung prüfen, sichtbare Collections zeigen
  collections  Collections mit Punktanzahl
  info         Vektor-Config, Payload-Schema, Punktanzahl einer Collection
  scroll       Punkte per Payload-Filter auflisten
  search       Semantische Suche (Embeddings nötig)
  version      Version anzeigen

Globale Option: --json (maschinenlesbare Ausgabe), bei jedem Befehl.
Hilfe zu einem Befehl: qdrantcli <befehl> --help

Umgebung: QDRANTCLI_URL, QDRANTCLI_TOKEN, QDRANTCLI_EMBED_URL, QDRANTCLI_EMBED_MODEL,
          QDRANTCLI_EMBED_KEY, QDRANTCLI_SERVER_MODEL, QDRANTCLI_CONFIG`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(rootUsage)
		return
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "login":
		cmdLogin(rest)
	case "whoami":
		cmdWhoami(rest)
	case "collections":
		cmdCollections(rest)
	case "info":
		cmdInfo(rest)
	case "scroll":
		cmdScroll(rest)
	case "search":
		cmdSearch(rest)
	case "version", "--version", "-v":
		fmt.Println("qdrantcli", version)
	default:
		fmt.Fprintf(os.Stderr, "Unbekannter Befehl %q\n\n%s\n", cmd, rootUsage)
		os.Exit(2)
	}
}

func cmdLogin(args []string) {
	fs := newFS("login", `
qdrantcli login – URL und Token speichern (Konfigdatei mit Rechten 0600)

Beispiele:
  qdrantcli login --url https://qdrant.firma.example
  qdrantcli login --url https://qdrant.firma.example --token "$MEIN_TOKEN"
  qdrantcli login --url https://qdrant.firma.example --embed-url https://llm.firma.example --embed-model text-embedding-3-small
  echo "$MEIN_TOKEN" | qdrantcli login --url https://qdrant.firma.example`)
	u := fs.String("url", "", "Qdrant-URL (Pflicht)")
	tok := fs.String("token", "", "Token (JWT oder API-Key); fehlt er, wird er von stdin gelesen")
	eu := fs.String("embed-url", "", "OpenAI-kompatibler Embedding-Endpunkt (Basis-URL)")
	em := fs.String("embed-model", "", "Embedding-Modell")
	sm := fs.String("server-model", "", "Modell für Qdrant-Server-Inferenz")
	noCheck := fs.Bool("no-check", false, "Verbindung nicht prüfen")
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	parseArgs(fs, args)
	if *u == "" {
		*u = os.Getenv("QDRANTCLI_URL")
	}
	if *u == "" {
		die(errors.New("--url fehlt"))
	}
	if _, err := url.ParseRequestURI(*u); err != nil {
		die(fmt.Errorf("ungültige URL %q", *u))
	}
	t := *tok
	if t == "" {
		t = os.Getenv("QDRANTCLI_TOKEN")
	}
	if t == "" {
		var err error
		t, err = readSecret("Token: ")
		if err != nil {
			die(err)
		}
	}
	old := Config{}
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &old)
		}
	}
	c := Config{URL: strings.TrimRight(*u, "/"), Token: t, EmbedURL: old.EmbedURL, EmbedModel: old.EmbedModel, ServerModel: old.ServerModel}
	if *eu != "" {
		c.EmbedURL = *eu
	}
	if *em != "" {
		c.EmbedModel = *em
	}
	if *sm != "" {
		c.ServerModel = *sm
	}
	var n int
	if !*noCheck {
		cl, _ := newClient(c)
		var r struct {
			Collections []struct{ Name string } `json:"collections"`
		}
		if err := cl.do("GET", "/collections", "", nil, &r); err != nil {
			die(fmt.Errorf("Login-Prüfung fehlgeschlagen: %v (mit --no-check trotzdem speichern)", err))
		}
		n = len(r.Collections)
	}
	p, err := saveConfig(c)
	if err != nil {
		die(fmt.Errorf("Konfig nicht gespeichert: %v", err))
	}
	if *asJSON {
		printJSON(map[string]any{"ok": true, "url": c.URL, "config": p, "collections": n})
		return
	}
	fmt.Printf("Gespeichert in %s\nURL: %s\n", p, c.URL)
	if !*noCheck {
		fmt.Printf("Verbindung ok, %d Collection(s) sichtbar\n", n)
	}
}

func listCollections(cl *client) ([]string, error) {
	var r struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
	}
	if err := cl.do("GET", "/collections", "", nil, &r); err != nil {
		return nil, err
	}
	var names []string
	for _, c := range r.Collections {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names, nil
}

func cmdWhoami(args []string) {
	fs := newFS("whoami", `
qdrantcli whoami – Verbindung prüfen und sichtbare Collections zeigen

Beispiele:
  qdrantcli whoami
  qdrantcli whoami --json`)
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	parseArgs(fs, args)
	c := loadConfig()
	cl, err := newClient(c)
	if err != nil {
		die(err)
	}
	names, err := listCollections(cl)
	if err != nil {
		die(err)
	}
	tokenKind := "keins"
	if c.Token != "" {
		tokenKind = "API-Key"
		if strings.Count(c.Token, ".") == 2 && strings.HasPrefix(c.Token, "eyJ") {
			tokenKind = "JWT"
		}
	}
	if *asJSON {
		if names == nil {
			names = []string{}
		}
		printJSON(map[string]any{"url": c.URL, "token_type": tokenKind, "collections": names})
		return
	}
	fmt.Printf("URL:    %s\nToken:  %s\nStatus: verbunden\nCollections (%d):\n", c.URL, tokenKind, len(names))
	for _, n := range names {
		fmt.Println("  -", n)
	}
}

func getCollection(cl *client, name string) (map[string]any, error) {
	var r map[string]any
	err := cl.do("GET", "/collections/"+url.PathEscape(name), name, nil, &r)
	return r, err
}

func num(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func cmdCollections(args []string) {
	fs := newFS("collections", `
qdrantcli collections – alle sichtbaren Collections mit Punktanzahl

Beispiele:
  qdrantcli collections
  qdrantcli collections --json`)
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	parseArgs(fs, args)
	cl, err := newClient(loadConfig())
	if err != nil {
		die(err)
	}
	names, err := listCollections(cl)
	if err != nil {
		die(err)
	}
	type row struct {
		Name   string `json:"name"`
		Points *int64 `json:"points_count"`
		Error  string `json:"error,omitempty"`
	}
	rows := []row{}
	for _, n := range names {
		r := row{Name: n}
		if info, err := getCollection(cl, n); err != nil {
			r.Error = err.Error()
		} else {
			p := num(info["points_count"])
			r.Points = &p
		}
		rows = append(rows, r)
	}
	if *asJSON {
		printJSON(rows)
		return
	}
	if len(rows) == 0 {
		fmt.Println("Keine Collections sichtbar.")
		return
	}
	w := 10
	for _, r := range rows {
		if len(r.Name) > w {
			w = len(r.Name)
		}
	}
	fmt.Printf("%-*s  %s\n", w, "COLLECTION", "PUNKTE")
	for _, r := range rows {
		if r.Error != "" {
			fmt.Printf("%-*s  (%s)\n", w, r.Name, r.Error)
		} else {
			fmt.Printf("%-*s  %d\n", w, r.Name, *r.Points)
		}
	}
}

func cmdInfo(args []string) {
	fs := newFS("info", `
qdrantcli info <collection> – Vektor-Config, Payload-Schema, Punktanzahl

Beispiele:
  qdrantcli info dokumente
  qdrantcli info dokumente --json`)
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	pos := parseArgs(fs, args)
	if len(pos) != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := pos[0]
	cl, err := newClient(loadConfig())
	if err != nil {
		die(err)
	}
	info, err := getCollection(cl, name)
	if err != nil {
		die(err)
	}
	var vectors any
	if cfg, ok := info["config"].(map[string]any); ok {
		if p, ok := cfg["params"].(map[string]any); ok {
			vectors = map[string]any{"vectors": p["vectors"], "sparse_vectors": p["sparse_vectors"]}
		}
	}
	if *asJSON {
		printJSON(map[string]any{
			"name": name, "status": info["status"], "points_count": num(info["points_count"]),
			"indexed_vectors_count": num(info["indexed_vectors_count"]),
			"vectors":               vectors, "payload_schema": info["payload_schema"],
		})
		return
	}
	fmt.Printf("Collection: %s\nStatus:     %v\nPunkte:     %d\n", name, info["status"], num(info["points_count"]))
	fmt.Println("Vektoren:")
	if vm, ok := vectors.(map[string]any); ok {
		printVectors(vm["vectors"])
		if sv, ok := vm["sparse_vectors"].(map[string]any); ok && len(sv) > 0 {
			for k := range sv {
				fmt.Printf("  %s: sparse\n", k)
			}
		}
	}
	fmt.Println("Payload-Schema:")
	ps, _ := info["payload_schema"].(map[string]any)
	if len(ps) == 0 {
		fmt.Println("  (keine Payload-Indizes)")
	}
	keys := make([]string, 0, len(ps))
	for k := range ps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := ps[k]
		if m, ok := t.(map[string]any); ok {
			fmt.Printf("  %s: %v (%d Punkte)\n", k, m["data_type"], num(m["points"]))
		} else {
			fmt.Printf("  %s: %s\n", k, compact(t))
		}
	}
}

func printVectors(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		fmt.Println("  (keine)")
		return
	}
	if _, single := m["size"]; single {
		fmt.Printf("  (unbenannt): Größe %d, Distanz %v\n", num(m["size"]), m["distance"])
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if vm, ok := m[k].(map[string]any); ok {
			fmt.Printf("  %s: Größe %d, Distanz %v\n", k, num(vm["size"]), vm["distance"])
		}
	}
}

type point struct {
	ID      any            `json:"id"`
	Score   *float64       `json:"score,omitempty"`
	Payload map[string]any `json:"payload"`
}

func printPoints(pts []point, fields []string, asJSON bool, extra map[string]any) {
	for i := range pts {
		pts[i].Payload = pickFields(pts[i].Payload, fields)
		if pts[i].Payload == nil {
			pts[i].Payload = map[string]any{}
		}
	}
	if asJSON {
		out := map[string]any{"points": pts}
		for k, v := range extra {
			out[k] = v
		}
		printJSON(out)
		return
	}
	if len(pts) == 0 {
		fmt.Println("Keine Treffer.")
		return
	}
	for _, p := range pts {
		if p.Score != nil {
			fmt.Printf("── id=%v  score=%.4f\n", p.ID, *p.Score)
		} else {
			fmt.Printf("── id=%v\n", p.ID)
		}
		keys := make([]string, 0, len(p.Payload))
		for k := range p.Payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := p.Payload[k]
			s, ok := v.(string)
			if !ok {
				s = compact(v)
			}
			fmt.Printf("  %s: %s\n", k, s)
		}
	}
	if extra != nil {
		if np, ok := extra["next_page_offset"]; ok && np != nil {
			fmt.Printf("\nWeitere Punkte vorhanden – nächster Offset: %v (--offset)\n", np)
		}
	}
}

func cmdScroll(args []string) {
	fs := newFS("scroll", `
qdrantcli scroll <collection> – Punkte per Payload-Filter auflisten (alle Filter müssen passen)

Beispiele:
  qdrantcli scroll dokumente --limit 5
  qdrantcli scroll dokumente --filter abteilung=vertrieb --filter jahr=2026
  qdrantcli scroll dokumente --fields titel,quelle --json`)
	var filters multiFlag
	fs.Var(&filters, "filter", "Payload-Filter key=value (mehrfach möglich)")
	limit := fs.Int("limit", 10, "maximale Anzahl Punkte")
	fields := fs.String("fields", "", "nur diese Payload-Felder ausgeben (kommagetrennt)")
	offset := fs.String("offset", "", "Start-Offset (Punkt-ID aus vorherigem Aufruf)")
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	pos := parseArgs(fs, args)
	if len(pos) != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := pos[0]
	f, err := buildFilter(filters)
	if err != nil {
		die(err)
	}
	cl, err := newClient(loadConfig())
	if err != nil {
		die(err)
	}
	body := map[string]any{"limit": *limit, "with_payload": true, "with_vector": false}
	if f != nil {
		body["filter"] = f
	}
	if *offset != "" {
		if i, err := strconv.ParseInt(*offset, 10, 64); err == nil {
			body["offset"] = i
		} else {
			body["offset"] = *offset
		}
	}
	var r struct {
		Points []point `json:"points"`
		Next   any     `json:"next_page_offset"`
	}
	if err := cl.do("POST", "/collections/"+url.PathEscape(name)+"/points/scroll", name, body, &r); err != nil {
		die(err)
	}
	if r.Points == nil {
		r.Points = []point{}
	}
	printPoints(r.Points, splitFields(*fields), *asJSON, map[string]any{"next_page_offset": r.Next})
}

func embedText(c Config, text string) ([]float64, error) {
	key := os.Getenv("QDRANTCLI_EMBED_KEY")
	u := strings.TrimRight(c.EmbedURL, "/")
	if !strings.HasSuffix(u, "/embeddings") {
		if !strings.HasSuffix(u, "/v1") {
			u += "/v1"
		}
		u += "/embeddings"
	}
	b, _ := json.Marshal(map[string]any{"model": c.EmbedModel, "input": text})
	req, err := http.NewRequest("POST", u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("Embedding-Endpunkt nicht erreichbar: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		s := strings.TrimSpace(string(data))
		if len(s) > 200 {
			s = s[:200]
		}
		return nil, fmt.Errorf("Embedding-Fehler %d: %s", resp.StatusCode, s)
	}
	var r struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &r); err != nil || len(r.Data) == 0 {
		return nil, errors.New("Embedding-Antwort ohne Vektor")
	}
	return r.Data[0].Embedding, nil
}

func cmdSearch(args []string) {
	fs := newFS("search", `
qdrantcli search <collection> "<text>" – semantische Suche

Der Suchtext braucht einen Vektor. Quellen (in dieser Reihenfolge):
  --vector-file    Datei mit rohem Vektor als JSON-Array, z. B. [0.1, 0.2, ...]
  --server-model   Qdrant-Server-Inferenz (POST /points/query mit {"text","model"})
  --embed-url + --embed-model   OpenAI-kompatibler /v1/embeddings-Endpunkt
                                (Key per QDRANTCLI_EMBED_KEY)
Die Embedding-Einstellungen lassen sich mit qdrantcli login speichern.

Beispiele:
  qdrantcli search dokumente "Reisekosten Richtlinie" --limit 5
  qdrantcli search dokumente "Urlaub" --filter abteilung=hr --embed-url https://llm.firma.example --embed-model text-embedding-3-small
  qdrantcli search dokumente "Urlaub" --server-model sentence-transformers/all-minilm-l6-v2
  qdrantcli search dokumente --vector-file vec.json --json`)
	var filters multiFlag
	fs.Var(&filters, "filter", "Payload-Filter key=value (mehrfach möglich)")
	limit := fs.Int("limit", 10, "maximale Anzahl Treffer")
	fields := fs.String("fields", "", "nur diese Payload-Felder ausgeben (kommagetrennt)")
	using := fs.String("using", "", "Name des Vektors bei benannten Vektoren")
	vecFile := fs.String("vector-file", "", "Datei mit rohem Vektor (JSON-Array)")
	serverModel := fs.String("server-model", "", "Modell für Qdrant-Server-Inferenz")
	embedURL := fs.String("embed-url", "", "OpenAI-kompatibler Embedding-Endpunkt")
	embedModel := fs.String("embed-model", "", "Embedding-Modell")
	asJSON := fs.Bool("json", false, "JSON-Ausgabe")
	pos := parseArgs(fs, args)
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		os.Exit(2)
	}
	name := pos[0]
	text := ""
	if len(pos) == 2 {
		text = pos[1]
	}
	c := loadConfig()
	if *serverModel != "" {
		c.ServerModel = *serverModel
	}
	if *embedURL != "" {
		c.EmbedURL = *embedURL
	}
	if *embedModel != "" {
		c.EmbedModel = *embedModel
	}
	f, err := buildFilter(filters)
	if err != nil {
		die(err)
	}
	var query any
	mode := ""
	switch {
	case *vecFile != "":
		b, err := os.ReadFile(*vecFile)
		if err != nil {
			die(fmt.Errorf("Vektor-Datei nicht lesbar: %v", err))
		}
		var v []float64
		if err := json.Unmarshal(b, &v); err != nil || len(v) == 0 {
			die(errors.New("Vektor-Datei muss ein JSON-Array aus Zahlen enthalten"))
		}
		query, mode = v, "vector-file"
	case text == "":
		die(errors.New("Suchtext fehlt (oder --vector-file angeben)"))
	case c.ServerModel != "" && *embedURL == "":
		query, mode = map[string]any{"text": text, "model": c.ServerModel}, "server-inference"
	case c.EmbedURL != "" && c.EmbedModel != "":
		v, err := embedText(c, text)
		if err != nil {
			die(err)
		}
		query, mode = v, "embed-url"
	default:
		var miss []string
		if c.EmbedURL == "" {
			miss = append(miss, "--embed-url")
		}
		if c.EmbedModel == "" {
			miss = append(miss, "--embed-model")
		}
		die(fmt.Errorf("keine Embedding-Quelle für Textsuche konfiguriert – es fehlt %s.\n"+
			"Optionen: --embed-url + --embed-model (Key: QDRANTCLI_EMBED_KEY), --server-model <modell> "+
			"oder --vector-file <datei>; dauerhaft per `qdrantcli login --url ... --embed-url ... --embed-model ...`",
			strings.Join(miss, " und ")))
	}
	cl, err := newClient(c)
	if err != nil {
		die(err)
	}
	body := map[string]any{"query": query, "limit": *limit, "with_payload": true, "with_vector": false}
	if f != nil {
		body["filter"] = f
	}
	if *using != "" {
		body["using"] = *using
	}
	var r struct {
		Points []point `json:"points"`
	}
	if err := cl.do("POST", "/collections/"+url.PathEscape(name)+"/points/query", name, body, &r); err != nil {
		die(err)
	}
	if r.Points == nil {
		r.Points = []point{}
	}
	printPoints(r.Points, splitFields(*fields), *asJSON, map[string]any{"mode": mode})
}
