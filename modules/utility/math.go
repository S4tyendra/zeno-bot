package utility

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"github.com/expr-lang/expr"

	"zeno/modules/utility/converter"
)

var (
	unitConverter *converter.Converter
	timeConvRegex = regexp.MustCompile(`(?i)^(now|unix|\d+)\s*(?:unix)?\s*(?:to|in)\s*([a-z_/]+)$`)
	currencyRegex = regexp.MustCompile(`(?i)^(\d*(?:\.\d+)?)?\s*([a-z]{3})\s*(?:to|in)\s*([a-z]{3})$`)
)

func init() {
	unitMap := converter.MustRegisterSystems()
	unitConverter = converter.NewConverter(unitMap)
	log.Println("[Math] NLP Unit Converter initialized")
}

func handleMath(m *telegram.NewMessage) error {
	query := m.Args()
	if query == "" {
		m.Reply("📐 **Zeno Math & Utility**\n\nUsage: `/math <expression>`\n\n**Examples:**\nConversion: `5 km to miles`, `100 f to c`\nCurrency: `10 USD to INR`, `500 EUR to USD`\nMath: `1+1`, `sqrt(25)`, `pow(2,3)`, `log(100)`, `log 3`\nTime: `now`, `1700000000` (unix timestamp), `now to IST`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	lowerQuery := strings.ToLower(query)

	// 1. Try Unit Converter
	if strings.Contains(lowerQuery, " in ") || strings.Contains(lowerQuery, " to ") {
		if result, err := unitConverter.Process(query); err == nil {
			valStr := fmt.Sprintf("%g", result.Value)
			m.Reply(fmt.Sprintf("📐 `%s` → **%s %s** (%s)", query, valStr, result.UnitSymbol, result.UnitName), &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
	}

	// 2. Try Currency Conversion
	if match := currencyRegex.FindStringSubmatch(strings.TrimSpace(query)); match != nil {
		amountStr := match[1]
		from := strings.ToUpper(match[2])
		to := strings.ToUpper(match[3])

		amount := 1.0
		if amountStr != "" {
			if val, err := strconv.ParseFloat(amountStr, 64); err == nil {
				amount = val
			}
		}

		converted, err := ConvertCurrency(amount, from, to)
		if err == nil {
			valStr := fmt.Sprintf("%.2f", converted)
			m.Reply(fmt.Sprintf("💱 `%g %s` = **%s %s**", amount, from, valStr, to), &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
	}

	// 3. Try Time Zone Conversion
	if match := timeConvRegex.FindStringSubmatch(strings.TrimSpace(query)); match != nil {
		timeStr := strings.ToLower(match[1])
		zoneStr := strings.ToLower(match[2])

		var t time.Time
		if timeStr == "now" {
			t = time.Now()
		} else {
			if ts, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
				if len(timeStr) == 13 {
					t = time.UnixMilli(ts)
				} else {
					t = time.Unix(ts, 0)
				}
			} else {
			}
		}

		if !t.IsZero() {
			locName, ok := TimeZoneMap[zoneStr]
			if !ok {
				if strings.Contains(zoneStr, "/") {
					caseMatch := timeConvRegex.FindStringSubmatch(strings.TrimSpace(query))
					locName = caseMatch[2]
				} else {
					locName = strings.ToUpper(zoneStr)
				}
			}

			loc, err := time.LoadLocation(locName)
			if err == nil {
				inZone := t.In(loc)
				m.Reply(fmt.Sprintf("🕒 **Time Conversion**\n\n**Input:** `%s`\n**Zone:** `%s`\n**Time:** `%s`", timeStr, locName, inZone.Format(time.RFC1123)), &telegram.SendOptions{ParseMode: "Markdown"})
				return nil
			}
		}
	}

	// 4. Try Timestamp (Unix) - Raw display
	if len(query) >= 10 && len(query) <= 13 && isNumeric(query) {
		if ts, err := strconv.ParseInt(query, 10, 64); err == nil {
			var t time.Time
			if len(query) == 13 {
				t = time.UnixMilli(ts)
			} else {
				t = time.Unix(ts, 0)
			}
			ist, _ := time.LoadLocation("Asia/Kolkata")
			var istStr string
			if ist != nil {
				istStr = t.In(ist).Format(time.RFC1123)
			} else {
				istStr = t.In(time.FixedZone("IST", 19800)).Format(time.RFC1123)
			}

			m.Reply(fmt.Sprintf("🕒 **Timestamp:** `%d`\n\n**UTC:** `%s`\n**IST:** `%s`\n**Relative:** `%s`", ts, t.UTC().Format(time.RFC1123), istStr, time.Since(t).String()), &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
	}

	if lowerQuery == "now" {
		now := time.Now()
		ist, _ := time.LoadLocation("Asia/Kolkata")
		var istStr string
		if ist != nil {
			istStr = now.In(ist).Format(time.RFC1123)
		} else {
			istStr = now.In(time.FixedZone("IST", 19800)).Format(time.RFC1123)
		}
		m.Reply(fmt.Sprintf("🕒 **Current Time**\n\n**UTC:** `%s`\n**IST:** `%s`\n**Unix:** `%d`", now.UTC().Format(time.RFC1123), istStr, now.Unix()), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	// 5. Try Math Evaluation (Expr) with Preprocessing
	mathQuery := preprocessMath(query)
	env := map[string]interface{}{
		"pi":    math.Pi,
		"e":     math.E,
		"sin":   math.Sin,
		"cos":   math.Cos,
		"tan":   math.Tan,
		"asin":  math.Asin,
		"acos":  math.Acos,
		"atan":  math.Atan,
		"sqrt":  math.Sqrt,
		"cbrt":  math.Cbrt,
		"pow":   math.Pow,
		"abs":   math.Abs,
		"ceil":  math.Ceil,
		"floor": math.Floor,
		"round": math.Round,
		"log":   math.Log10,
		"ln":    math.Log,
		"deg":   func(r float64) float64 { return r * 180 / math.Pi },
		"rad":   func(d float64) float64 { return d * math.Pi / 180 },
		"max":   math.Max,
		"min":   math.Min,
	}

	program, err := expr.Compile(mathQuery, expr.Env(env))
	if err == nil {
		output, err := expr.Run(program, env)
		if err == nil {
			m.Reply(fmt.Sprintf("🧮 `%s` = **%v**", query, output), &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
	} else {
		// Log error for debugging if needed, but don't reply yet
		// fmt.Println("Expr compile error:", err)
	}

	// 6. Try Unit Converter fallback
	if result, err := unitConverter.Process(query); err == nil {
		valStr := fmt.Sprintf("%g", result.Value)
		m.Reply(fmt.Sprintf("📐 `%s` → **%s %s** (%s)", query, valStr, result.UnitSymbol, result.UnitName), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	} else {
		if strings.Contains(err.Error(), "unknown unit") {
			m.Reply(fmt.Sprintf("❌ %s", err.Error()))
			return nil
		}
	}

	m.Reply("❌ Could not process input. Try `/math 1+1` or `/math 1km to miles`.")
	return nil
}

func preprocessMath(query string) string {
	q := strings.ToLower(query)
	// Fix: "log 3" -> "log(3)", "sin 30" -> "sin(30)", "sqrt2" -> "sqrt(2)"
	// Supports optional space between match and number.
	// NOTE: Does not assume user typed brackets. If user typed "log(3)", regex won't match "3" as immediate neighbor to "log" because of "(".
	// Regex: Word boundary + Function Name + Optional Space + Number (float/int)
	funcs := "log|ln|sqrt|sin|cos|tan|asin|acos|atan|cbrt|abs|ceil|floor|round|deg|rad|max|min"
	re := regexp.MustCompile(fmt.Sprintf(`\b(%s)\s*(\d+(?:\.\d+)?)`, funcs))
	q = re.ReplaceAllString(q, "$1($2)")
	return q
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
