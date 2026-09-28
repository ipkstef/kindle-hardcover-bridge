package screen

import (
	"encoding/json"
	"testing"
)

func TestAlertParams(t *testing.T) {
	var v struct {
		CP struct {
			AlertID  string `json:"alertId"`
			Show     bool   `json:"show"`
			AutoHide int    `json:"autoHide"`
			Strings  []struct {
				Match   string `json:"matchStr"`
				Replace string `json:"replaceStr"`
			} `json:"customStrings"`
		} `json:"clientParams"`
	}
	if err := json.Unmarshal([]byte(AlertParams("Hardcover", `4.5 ★ "Dune"`, 6000)), &v); err != nil {
		t.Fatal(err)
	}
	if v.CP.AlertID != "appAlert1" || !v.CP.Show || v.CP.AutoHide != 6000 || len(v.CP.Strings) != 2 ||
		v.CP.Strings[1].Replace != `4.5 ★ "Dune"` {
		t.Fatalf("%+v", v)
	}
}
