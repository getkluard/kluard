package config

import "testing"

func TestOldLumenSettingsStillWork(t *testing.T) {
	t.Setenv("LUMEN_SECRET_KEY", "old-secret")
	t.Setenv("LUMEN_ADMIN_USER", "oldadmin")
	t.Setenv("KLUARD_ADMIN_USER", "newadmin")
	c := Load()
	if c.SecretKey != "old-secret" {
		t.Fatalf("a setting with the old LUMEN_ name must still be read, got %q", c.SecretKey)
	}
	if c.AdminUser != "newadmin" {
		t.Fatalf("the new name wins when both are set, got %q", c.AdminUser)
	}
	if c.ClickHouseDB != "lumen" || c.ESPrefix != "lumen" {
		t.Fatalf("the database and index names keep their old default so that existing data is found: %q %q", c.ClickHouseDB, c.ESPrefix)
	}
}
