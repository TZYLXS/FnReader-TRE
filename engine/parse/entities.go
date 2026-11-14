package parse

import (
	"strconv"
	"strings"
)

// 常用 HTML 实体。EPUB / MOBI 正文里主要是 nbsp、amp、lt、gt 和标点实体，
// 未收录的实体保持原样（服务端 cheerio 会解码，但未收录实体很少见）。
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"nbsp": "\u00a0", "ensp": "\u2002", "emsp": "\u2003", "thinsp": "\u2009",
	"ndash": "–", "mdash": "—", "hellip": "…", "middot": "·", "bull": "•",
	"lsquo": "‘", "rsquo": "’", "ldquo": "“", "rdquo": "”", "laquo": "«", "raquo": "»",
	"copy": "©", "reg": "®", "trade": "™", "deg": "°", "plusmn": "±",
	"times": "×", "divide": "÷", "frac12": "½", "frac14": "¼", "frac34": "¾",
	"sup2": "²", "sup3": "³", "micro": "µ", "para": "¶", "sect": "§",
	"dagger": "†", "Dagger": "‡", "permil": "‰", "prime": "′", "Prime": "″",
	"euro": "€", "pound": "£", "yen": "¥", "cent": "¢", "curren": "¤",
	"iexcl": "¡", "iquest": "¿", "szlig": "ß", "agrave": "à", "aacute": "á",
	"acirc": "â", "atilde": "ã", "auml": "ä", "aring": "å", "aelig": "æ", "ccedil": "ç",
	"egrave": "è", "eacute": "é", "ecirc": "ê", "euml": "ë", "igrave": "ì", "iacute": "í",
	"icirc": "î", "iuml": "ï", "ntilde": "ñ", "ograve": "ò", "oacute": "ó", "ocirc": "ô",
	"otilde": "õ", "ouml": "ö", "oslash": "ø", "ugrave": "ù", "uacute": "ú", "ucirc": "û",
	"uuml": "ü", "yacute": "ý", "yuml": "ÿ", "szacute": "ś", "alpha": "α", "beta": "β",
	"gamma": "γ", "delta": "δ", "epsilon": "ε", "lambda": "λ", "mu": "μ", "pi": "π",
	"sigma": "σ", "omega": "ω", "infin": "∞", "ne": "≠", "le": "≤", "ge": "≥",
	"larr": "←", "rarr": "→", "uarr": "↑", "darr": "↓", "harr": "↔",
	"loz": "◊", "spades": "♠", "clubs": "♣", "hearts": "♥", "diams": "♦",
}

// decodeEntities 解码命名实体与数字实体；不认识的命名实体原样保留。
func decodeEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			b.WriteByte(s[i])
			continue
		}
		// 找分号，实体最长 32 字节
		end := -1
		for j := i + 1; j < len(s) && j <= i+32; j++ {
			if s[j] == ';' {
				end = j
				break
			}
		}
		if end < 0 {
			b.WriteByte('&')
			continue
		}
		body := s[i+1 : end]
		if body == "" {
			b.WriteByte('&')
			continue
		}
		if body[0] == '#' {
			num := body[1:]
			base := 10
			if len(num) > 1 && (num[0] == 'x' || num[0] == 'X') {
				base = 16
				num = num[1:]
			}
			v, err := strconv.ParseInt(num, base, 32)
			if err == nil && v > 0 && v <= 0x10FFFF {
				b.WriteRune(rune(v))
				i = end
				continue
			}
			b.WriteByte('&')
			continue
		}
		if v, ok := namedEntities[body]; ok {
			b.WriteString(v)
			i = end
			continue
		}
		b.WriteByte('&')
	}
	return b.String()
}
