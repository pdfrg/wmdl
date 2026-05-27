package review

import "strings"

func countryFlag(code string) string {
	if len(code) != 2 {
		return ""
	}
	code = strings.ToUpper(code)
	r1 := 0x1F1E6 + int(rune(code[0])-'A')
	r2 := 0x1F1E6 + int(rune(code[1])-'A')
	return string(rune(r1)) + string(rune(r2))
}

var languageNames = map[string]string{
	"ab": "Abkhazian", "aa": "Afar", "af": "Afrikaans",
	"sq": "Albanian", "am": "Amharic", "ar": "Arabic",
	"hy": "Armenian", "as": "Assamese", "ay": "Aymara",
	"az": "Azerbaijani", "ba": "Bashkir", "eu": "Basque",
	"bn": "Bengali", "dz": "Dzongkha", "bh": "Bihari",
	"bi": "Bislama", "br": "Breton", "bg": "Bulgarian",
	"my": "Burmese", "be": "Belarusian", "km": "Cambodian",
	"ca": "Catalan", "zh": "Chinese", "co": "Corsican",
	"hr": "Croatian", "cs": "Czech", "da": "Danish",
	"nl": "Dutch", "en": "English", "eo": "Esperanto",
	"et": "Estonian", "fo": "Faroese", "fj": "Fijian",
	"fi": "Finnish", "fr": "French", "fy": "Frisian",
	"gl": "Galician", "ka": "Georgian", "de": "German",
	"el": "Greek", "kl": "Greenlandic", "gn": "Guarani",
	"gu": "Gujarati", "ha": "Hausa", "he": "Hebrew",
	"hi": "Hindi", "hu": "Hungarian", "is": "Icelandic",
	"id": "Indonesian", "ia": "Interlingua", "ie": "Interlingue",
	"iu": "Inuktitut", "ik": "Inupiak", "ga": "Irish",
	"it": "Italian", "ja": "Japanese", "jv": "Javanese",
	"kn": "Kannada", "ks": "Kashmiri", "kk": "Kazakh",
	"rw": "Kinyarwanda", "ky": "Kirghiz", "rn": "Kirundi",
	"ko": "Korean", "ku": "Kurdish", "lo": "Lao",
	"la": "Latin", "lv": "Latvian", "li": "Limburgish",
	"ln": "Lingala", "lt": "Lithuanian", "mk": "Macedonian",
	"mg": "Malagasy", "ms": "Malay", "ml": "Malayalam",
	"mt": "Maltese", "mi": "Maori", "mr": "Marathi",
	"mo": "Moldavian", "mn": "Mongolian", "na": "Nauru",
	"ne": "Nepali", "no": "Norwegian", "oc": "Occitan",
	"or": "Oriya", "om": "Oromo", "ps": "Pashto",
	"fa": "Persian", "pl": "Polish", "pt": "Portuguese",
	"pa": "Punjabi", "qu": "Quechua", "rm": "Rhaeto-Romance",
	"ro": "Romanian", "ru": "Russian", "sm": "Samoan",
	"sg": "Sango", "sa": "Sanskrit", "sr": "Serbian",
	"sh": "Serbo-Croatian", "st": "Sesotho", "tn": "Setswana",
	"sn": "Shona", "sd": "Sindhi", "si": "Sinhalese",
	"ss": "Siswati", "sk": "Slovak", "sl": "Slovenian",
	"so": "Somali", "es": "Spanish", "su": "Sundanese",
	"sw": "Swahili", "sv": "Swedish", "tl": "Tagalog",
	"tg": "Tajik", "ta": "Tamil", "tt": "Tatar",
	"te": "Telugu", "th": "Thai", "bo": "Tibetan",
	"ti": "Tigrinya", "to": "Tonga", "ts": "Tsonga",
	"tr": "Turkish", "tk": "Turkmen", "tw": "Twi",
	"ug": "Uighur", "uk": "Ukrainian", "ur": "Urdu",
	"uz": "Uzbek", "vi": "Vietnamese", "vo": "Volapuk",
	"cy": "Welsh", "wo": "Wolof", "xh": "Xhosa",
	"yi": "Yiddish", "yo": "Yoruba", "zu": "Zulu",
}

func languageName(code string) string {
	if code == "" {
		return ""
	}
	code = strings.ToLower(code)
	if name, ok := languageNames[code]; ok {
		return name
	}
	return strings.ToUpper(code)
}
