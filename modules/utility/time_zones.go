package utility

var TimeZoneMap = map[string]string{
	// Common Abbreviations
	"ist":   "Asia/Kolkata",
	"utc":   "UTC",
	"gmt":   "Etc/GMT",
	"est":   "America/New_York",
	"edt":   "America/New_York",
	"cst":   "America/Chicago",
	"cdt":   "America/Chicago",
	"mst":   "America/Denver",
	"mdt":   "America/Denver",
	"pst":   "America/Los_Angeles",
	"pdt":   "America/Los_Angeles",
	"cet":   "Europe/Paris",
	"cest":  "Europe/Paris",
	"bst":   "Europe/London",
	"jst":   "Asia/Tokyo",
	"kst":   "Asia/Seoul",
	"aest":  "Australia/Sydney",
	"aedt":  "Australia/Sydney",
	"nzst":  "Pacific/Auckland",
	"nzdt":  "Pacific/Auckland",
	"sgt":   "Asia/Singapore",
	"hkt":   "Asia/Hong_Kong",
	"china": "Asia/Shanghai",

	// US Timezones
	"us/eastern":  "America/New_York",
	"us/central":  "America/Chicago",
	"us/mountain": "America/Denver",
	"us/pacific":  "America/Los_Angeles",
	"us/alaska":   "America/Anchorage",
	"us/hawaii":   "Pacific/Honolulu",

	// Europe
	"london": "Europe/London",
	"paris":  "Europe/Paris",
	"berlin": "Europe/Berlin",
	"moscow": "Europe/Moscow",

	// Asia
	"dubai":   "Asia/Dubai",
	"mumbai":  "Asia/Kolkata",
	"delhi":   "Asia/Kolkata",
	"bangkok": "Asia/Bangkok",
	"tokyo":   "Asia/Tokyo",
	"seoul":   "Asia/Seoul",

	// Australia
	"sydney":    "Australia/Sydney",
	"melbourne": "Australia/Melbourne",
	"perth":     "Australia/Perth",
}
