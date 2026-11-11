package agent

import "testing"

func TestGuessSeverity(t *testing.T) {
	for line, want := range map[string]string{
		`192.168.20.1 - admin [11/Nov/2026:11:15:37 +0000] "PROPFIND /remote.php/dav/files/admin/ HTTP/1.0" 207 1934 "-" "Go-http-client/2.0"`: "INFO",
		`83.250.139.255 - - [11/Nov/2026:11:15:44 +0000] "GET /ocs/v2.php/x HTTP/1.0" 304 709 "-" "Mozilla/5.0"`:                               "INFO",
		`1.2.3.4 - - [11/Nov/2026:11:15:44 +0000] "GET /missing HTTP/1.1" 404 153 "-" "curl"`:                                                  "WARN",
		`1.2.3.4 - - [11/Nov/2026:11:15:44 +0000] "POST /api HTTP/1.1" 502 157 "-" "curl"`:                                                     "ERROR",
		`2026/11/11 11:15:44 [error] 29#29: *1 connect() failed (111: Connection refused)`:                                                     "ERROR",
		`2026/11/11 11:15:44 [warn] 29#29: *1 upstream server temporarily disabled`:                                                            "WARN",
		`2026/11/11 11:15:44 [emerg] 1#1: bind() to 0.0.0.0:80 failed`:                                                                         "FATAL",
		`time="2026-11-11T11:15:44Z" level=warning msg="slow"`:                                                                                 "WARN",
		`{"level":"error","msg":"boom"}`:                                      "ERROR",
		`2026-11-11 11:15:44.123 UTC [1] LOG:  database system is ready`:      "",
		`2026-11-11 11:15:44.123 UTC [1] ERROR:  relation "x" does not exist`: "ERROR",
		`crond: USER www-data pid 4819 cmd php -f /var/www/html/cron.php`:     "",
		`INFO: started worker 3`:                                              "INFO",
		`everything went fine, no error found`:                                "",
		`The message contains the word Error in sentence case only`:           "",
	} {
		if got := guessSeverity(line); got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}
