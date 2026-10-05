package weir

// TrackingParams returns Key.QueryDrop patterns for the click and campaign
// identifiers that ad networks and mail tools append to links. They rarely
// change a response, and each distinct value would otherwise mint a key.
// Weir drops nothing by default (D30); opt in with
//
//	weir.Config{Key: weir.KeyConfig{QueryDrop: weir.TrackingParams()}}
//
// Dropped parameters never reach the origin (FR-KEY-5), so leave out any
// name the origin reads. Each call returns a new slice that the caller may
// extend or trim.
func TrackingParams() []string {
	return []string{
		"utm_*",                                        // Google Analytics campaigns
		"gclid", "gclsrc", "dclid", "gbraid", "wbraid", // Google Ads
		"_ga", "_gl", // Google Analytics cross-domain linker
		"srsltid",          // Google Merchant Center
		"fbclid",           // Meta
		"msclkid",          // Microsoft Ads
		"twclid",           // X
		"ttclid",           // TikTok
		"li_fat_id",        // LinkedIn
		"igshid",           // Instagram
		"yclid",            // Yandex
		"mc_cid", "mc_eid", // Mailchimp
		"_hsenc", "_hsmi", // HubSpot
		"mkt_tok", // Marketo
	}
}
