package agent

import (
	"regexp"
	"strings"
)

// Most log files do not say how serious a line is. Rather than show everything as "unspecified", the agent makes a
// careful guess from what the line itself says: the status code of a web access line, or a level word near the start
// of the line. A line that gives no hint stays unspecified: a wrong guess is worse than none.

// accessStatus matches the end of the request in a web server access log (Apache, nginx, Caddy common formats):
// "GET /path HTTP/1.1" 502 1234
var accessStatus = regexp.MustCompile(`"[A-Z]{3,10} [^"]* HTTP/[0-9.]+" ([1-5][0-9][0-9]) `)

// levelWord matches a level written as a word in a place where it is clearly a level: [error], level=warn,
// "level":"info", ERROR: or a bare upper-case word.
var levelWord = regexp.MustCompile(`(?i)(?:\[|\blevel["']?\s*[:=]\s*["']?|\bseverity["']?\s*[:=]\s*["']?|\blvl=)(trace|debug|info|notice|warn|warning|error|err|crit|critical|alert|emerg|fatal|panic)\b`)
var upperWord = regexp.MustCompile(`\b(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|CRITICAL|FATAL|PANIC)\b`)

var levelName = map[string]string{
	"trace": "DEBUG", "debug": "DEBUG", "info": "INFO", "notice": "INFO", "warn": "WARN", "warning": "WARN",
	"error": "ERROR", "err": "ERROR", "crit": "FATAL", "critical": "FATAL", "alert": "FATAL", "emerg": "FATAL", "fatal": "FATAL", "panic": "FATAL",
}

// guessSeverity returns DEBUG, INFO, WARN, ERROR or FATAL, or "" when the line gives no hint.
func guessSeverity(line string) string {
	if m := accessStatus.FindStringSubmatch(line); m != nil {
		switch m[1][0] {
		case '5':
			return "ERROR"
		case '4':
			return "WARN"
		default:
			return "INFO"
		}
	}
	head := line
	if len(head) > 160 {
		head = head[:160] // a level is written early; a word deep inside a message ("no error found") is not one
	}
	if m := levelWord.FindStringSubmatch(head); m != nil {
		return levelName[strings.ToLower(m[1])]
	}
	if m := upperWord.FindStringSubmatch(head); m != nil {
		return levelName[strings.ToLower(m[1])]
	}
	return ""
}
