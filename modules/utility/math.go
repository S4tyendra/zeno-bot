package utility

import (
	"fmt"
	"log"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/modules/utility/converter"
)

var unitConverter *converter.Converter

func init() {
	unitMap := converter.MustRegisterSystems()
	unitConverter = converter.NewConverter(unitMap)
	log.Println("[Math] NLP Unit Converter initialized")
}

func handleMath(m *telegram.NewMessage) error {
	query := m.Args()
	if query == "" {
		m.Reply("📐 **NLP Unit Converter**\n\nUsage: `/math <expression>`\n\nExamples:\n`/math 5 km to miles`\n`/math 100 f to c`\n`/math 2 gallons + 1 pint in liters`\n`/math two pounds and 8 ounces to grams`\n`/math 100 sqft to m2`\n`/math 60 mph to kph`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	result, err := unitConverter.Process(query)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "Did you mean") {
			m.Reply(fmt.Sprintf("❌ %s", errMsg))
		} else {
			m.Reply(fmt.Sprintf("❌ %s", errMsg))
		}
		return nil
	}

	// Format value — strip trailing zeros
	valStr := fmt.Sprintf("%g", result.Value)

	m.Reply(fmt.Sprintf("📐 `%s` → **%s %s** (%s)", query, valStr, result.UnitSymbol, result.UnitName), &telegram.SendOptions{
		ParseMode: "Markdown",
	})
	return nil
}
