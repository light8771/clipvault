package clipvault

import (
	"bytes"
	"strings"
)

type compositeRTFFeatures struct {
	image, table, link, object bool
}

func inspectCompositeRTF(raw []byte) (string, compositeRTFFeatures, error) {
	var features compositeRTFFeatures
	text, err := parseRTFTextWithControls(raw, func(word string, remaining []byte) {
		switch word {
		case "pict":
			features.image = true
		case "trowd":
			features.table = true
		case "object", "objdata":
			features.object = true
		case "fldinst":
			instruction := bytes.TrimLeft(remaining, " \t\r\n")
			if len(instruction) > 9 && strings.EqualFold(string(instruction[:9]), "HYPERLINK") && bytes.ContainsRune([]byte(" \t\r\n"), rune(instruction[9])) {
				features.link = true
			}
		}
	})
	return text, features, err
}
