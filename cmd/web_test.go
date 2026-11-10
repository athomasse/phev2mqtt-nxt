package cmd

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
)

func (f *fakeBroker) Disconnect(quiesce uint) {}

func setConfig(t *testing.T, key string, value interface{}) {
	t.Helper()
	configMu.Lock()
	viper.Set(key, value)
	configMu.Unlock()
	t.Cleanup(func() {
		configMu.Lock()
		viper.Set(key, nil)
		configMu.Unlock()
	})
}

func newTestWeb(t *testing.T, password string) (*webServer, *mqttClient) {
	t.Helper()
	m, _ := newTestClient("phev")
	ws, err := newWebServer(m, "127.0.0.1:0", password, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(log.LevelHooks{}) })
	return ws, m
}

func request(t *testing.T, ws *webServer, method, path, body, password string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if password != "" {
		req.SetBasicAuth("admin", password)
	}
	if csrf {
		req.Header.Set("X-Requested-With", "phev2mqtt")
	}
	rec := httptest.NewRecorder()
	ws.handler.ServeHTTP(rec, req)
	return rec
}

func TestWebListenNeedsPasswordOffLocalhost(t *testing.T) {
	m, _ := newTestClient("phev")
	defer log.StandardLogger().ReplaceHooks(log.LevelHooks{})
	for _, tc := range []struct {
		listen, password string
		auth, ok         bool
	}{
		{"127.0.0.1:8080", "", true, true},
		{"localhost:8080", "", true, true},
		{"[::1]:8080", "", true, true},
		{":8080", "", true, false},
		{"0.0.0.0:8080", "", true, false},
		{"192.168.1.40:8080", "", true, false},
		{":8080", "secret", true, true},
		{":8080", "", false, true},
		{"8080", "secret", true, false},
	} {
		_, err := newWebServer(m, tc.listen, tc.password, tc.auth)
		if (err == nil) != tc.ok {
			t.Errorf("listen %q password %q auth %v: err=%v, want ok=%v", tc.listen, tc.password, tc.auth, err, tc.ok)
		}
	}
}

func TestWebAuth(t *testing.T) {
	ws, _ := newTestWeb(t, "secret")
	if rec := request(t, ws, "GET", "/api/state", "", "", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("no password: got %d, want 401", rec.Code)
	}
	if rec := request(t, ws, "GET", "/api/state", "", "wrong", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", rec.Code)
	}
	if rec := request(t, ws, "GET", "/api/state", "", "secret", false); rec.Code != http.StatusOK {
		t.Errorf("right password: got %d, want 200", rec.Code)
	}
	if rec := request(t, ws, "GET", "/", "", "secret", false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>phev2mqtt</title>") {
		t.Errorf("index: got %d", rec.Code)
	}
}

func TestWebWithoutAuth(t *testing.T) {
	m, _ := newTestClient("phev")
	defer log.StandardLogger().ReplaceHooks(log.LevelHooks{})
	ws, err := newWebServer(m, ":0", "ignored", false)
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(t, ws, "GET", "/api/state", "", "", false); rec.Code != http.StatusOK {
		t.Errorf("web_auth false: got %d, want 200", rec.Code)
	}
	if rec := request(t, ws, "POST", "/api/command", `{"command":"headlights","value":"on"}`, "", false); rec.Code != http.StatusForbidden {
		t.Errorf("web_auth false still needs X-Requested-With: got %d, want 403", rec.Code)
	}
}

func TestWebPostNeedsHeader(t *testing.T) {
	ws, _ := newTestWeb(t, "")
	body := `{"command":"headlights","value":"on"}`
	if rec := request(t, ws, "POST", "/api/command", body, "", false); rec.Code != http.StatusForbidden {
		t.Errorf("without X-Requested-With: got %d, want 403", rec.Code)
	}
}

func TestWebCommand(t *testing.T) {
	for _, tc := range []struct {
		body     string
		code     int
		register byte // 0: nothing may reach the car
	}{
		{`{"command":"headlights","value":"on"}`, http.StatusAccepted, 0x0a},
		{`{"command":"climate_heat","value":"20"}`, http.StatusAccepted, protocol.SetACModeRegisterMY18},
		{`{"command":"headlights","value":"blink"}`, http.StatusBadRequest, 0},
		{`{"command":"register","value":"0a"}`, http.StatusBadRequest, 0},
		{`{"command":"headlights","value":"on","extra":1}`, http.StatusBadRequest, 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			ws, m := newTestWeb(t, "")
			cl, err := client.New()
			if err != nil {
				t.Fatal(err)
			}
			m.state.setCar(cl)
			m.state.setConnected(true, client.ModelYear18)
			defer cl.Close()

			rec := request(t, ws, "POST", "/api/command", tc.body, "", true)
			if rec.Code != tc.code {
				t.Fatalf("got %d (%s), want %d", rec.Code, rec.Body, tc.code)
			}
			select {
			case msg := <-cl.Send:
				if tc.register == 0 {
					t.Errorf("sent register %02x, want nothing", msg.Register)
				} else if msg.Register != tc.register {
					t.Errorf("sent register %02x, want %02x", msg.Register, tc.register)
				}
			case <-time.After(500 * time.Millisecond):
				if tc.register != 0 {
					t.Errorf("nothing sent, want register %02x", tc.register)
				}
			}
		})
	}
}

func TestWebMQTTSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "settings.yaml")
	setConfig(t, "web_settings_file", file)
	setConfig(t, "mqtt_password", "from-config-file")
	ws, _ := newTestWeb(t, "")

	rec := request(t, ws, "GET", "/api/mqtt", "", "", false)
	if strings.Contains(rec.Body.String(), "from-config-file") {
		t.Fatal("GET /api/mqtt returned the password")
	}
	var got mqttSettings
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.PasswordSet {
		t.Error("password_set: got false, want true")
	}

	save := `{"server":"tcp://127.0.0.1:9","username":"u","topic_prefix":"car","ha_discovery":false,"ha_discovery_prefix":"homeassistant"}`
	if rec := request(t, ws, "POST", "/api/mqtt", save, "", true); rec.Code != http.StatusOK {
		t.Fatalf("save: got %d (%s)", rec.Code, rec.Body)
	}
	if cfgString("mqtt_server") != "tcp://127.0.0.1:9" || cfgString("mqtt_topic_prefix") != "car" || cfgBool("ha_discovery") {
		t.Errorf("settings not applied: server=%q prefix=%q", cfgString("mqtt_server"), cfgString("mqtt_topic_prefix"))
	}
	if cfgString("mqtt_password") != "from-config-file" {
		t.Error("a save without password changed the password")
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("settings file mode %v, want 0600", info.Mode().Perm())
	}

	for _, bad := range []string{
		`{"server":"192.168.1.10:1883","topic_prefix":"p","ha_discovery_prefix":"h"}`,
		`{"server":"http://x:1883","topic_prefix":"p","ha_discovery_prefix":"h"}`,
		`{"server":"tcp://x:1883","topic_prefix":"p/#","ha_discovery_prefix":"h"}`,
	} {
		if rec := request(t, ws, "POST", "/api/mqtt", bad, "", true); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", bad, rec.Code)
		}
	}
}

func TestReadWebSettingsIgnoresOtherKeys(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.yaml")
	os.WriteFile(file, []byte("mqtt_server: tcp://b:1883\naddress: 10.0.0.1:1\nweb_password: x\n"), 0o600)
	got, err := readWebSettings(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["mqtt_server"] != "tcp://b:1883" {
		t.Errorf("got %v, want only mqtt_server", got)
	}
}

func TestLogRing(t *testing.T) {
	r := newLogRing(3)
	for _, msg := range []string{"a", "b", "c", "d"} {
		r.Fire(&log.Entry{Message: msg, Level: log.InfoLevel, Data: log.Fields{"k": 1}})
	}
	lines := r.since(0)
	if len(lines) != 3 || lines[0].Message != "b k=1" || lines[2].Seq != 4 {
		t.Fatalf("got %+v", lines)
	}
	if got := r.since(3); len(got) != 1 || got[0].Message != "d k=1" {
		t.Errorf("since(3): got %+v", got)
	}
}

func TestParseNmcli(t *testing.T) {
	if got := splitTerse(`*:My\:Net:80:WPA2`); strings.Join(got, "|") != "*|My:Net|80|WPA2" {
		t.Errorf("splitTerse: got %q", got)
	}
	nets := parseWifiList(" :REMOTE12ab:40:WPA2\n*:REMOTE12ab:70:WPA2\n :Home:55:WPA2\n ::90:WPA2\n")
	if len(nets) != 2 || nets[0].SSID != "REMOTE12ab" || nets[0].Signal != 70 || !nets[0].Active || nets[1].SSID != "Home" {
		t.Errorf("parseWifiList: got %+v", nets)
	}
	v := terseValues("GENERAL.STATE:100 (connected)\nIP4.ADDRESS[1]:192.168.8.47/24\n")
	if v["GENERAL.STATE"] != "100 (connected)" || v["IP4.ADDRESS[1]"] != "192.168.8.47/24" {
		t.Errorf("terseValues: got %v", v)
	}
}

func TestConnectCarValidation(t *testing.T) {
	w := &wifiManager{iface: "wlan0"}
	for _, tc := range [][3]string{
		{"", "password1", ""},
		{strings.Repeat("x", 33), "password1", ""},
		{"REMOTE", "short", ""},
		{"REMOTE", "password1", "not-a-mac"},
	} {
		if err := w.connectCar(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("connectCar(%q, %q, %q): no error", tc[0], tc[1], tc[2])
		}
	}
}

func TestWebEvents(t *testing.T) {
	ws, m := newTestWeb(t, "")
	srv := httptest.NewServer(ws.handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				events <- sc.Text()
			}
		}
	}()
	next := func() string {
		select {
		case e := <-events:
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	next() // Initial state.
	m.publish("/battery/level", "77")
	for {
		if e := next(); strings.Contains(e, `"/battery/level":"77"`) {
			return
		}
	}
}
