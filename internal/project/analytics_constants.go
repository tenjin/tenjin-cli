package project

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	dollarTemplate = regexp.MustCompile(`\$[A-Za-z_{]`)
	stringLiteral  = regexp.MustCompile(`^@?(?:"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|` + "`[^`]*`" + `)$`)
	unescape       = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`)
)

// literal returns the value of a string literal that is the whole of code,
// and false for anything else, including a string built from variables.
func literal(code, ext string) (string, bool) {
	raw := ext == ".dart" && (strings.HasPrefix(code, "r'") || strings.HasPrefix(code, `r"`))
	if raw {
		code = code[1:]
	}
	if !stringLiteral.MatchString(code) {
		return "", false
	}
	body := strings.TrimPrefix(code, "@")
	quote := body[0]
	body = body[1 : len(body)-1]
	if raw {
		return body, true
	}
	if interpolates(body, quote, ext) {
		return "", false
	}
	return unescape.Replace(body), true
}

// interpolates reports whether a string literal's body takes values in, as
// the language of the file reads it.
func interpolates(body string, quote byte, ext string) bool {
	switch ext {
	case ".kt", ".dart":
		return dollarTemplate.MatchString(body)
	case ".swift":
		return strings.Contains(body, `\(`)
	}
	return isJS(ext) && quote == '`' && strings.Contains(body, "${")
}

var (
	constantLike = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$`)
	upperSnake   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	kConstant    = regexp.MustCompile(`^k[A-Z]\w*$`)
	stringValue  = `@?("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')`
	assignment   = regexp.MustCompile(`\b(\w+)\s*(?::\s*[\w.?<>]+\s*)?=\s*` + stringValue + `\s*(?:[;,)}\n]|$)`)
	property     = regexp.MustCompile(`\b(\w+)\s*:\s*` + stringValue + `\s*(?:[,}\n]|$)`)
)

// constantTable holds the names the app's code sets to a string literal,
// with the values each is set to: by the container they are declared in
// (Events.LEVEL_UP), outside any container, and by name alone.
type constantTable struct {
	qualified, topLevel, any map[string]map[string]bool
}

// table reads the app's constants, the first time one is needed.
func (s *analyticsScan) table() *constantTable {
	if s.constants != nil {
		return s.constants
	}
	t := &constantTable{qualified: map[string]map[string]bool{}, topLevel: map[string]map[string]bool{}, any: map[string]map[string]bool{}}
	put := func(into map[string]map[string]bool, key, value string) {
		if into[key] == nil {
			into[key] = map[string]bool{}
		}
		into[key][value] = true
	}
	for _, f := range s.files {
		add := func(m []int) {
			value, ok := literal(f.text[m[4]:m[5]], f.ext)
			if !ok || value == "" {
				return
			}
			name := f.text[m[2]:m[3]]
			if in := f.innermost(m[2]); in != nil {
				put(t.qualified, in.name+"."+name, value)
			} else {
				put(t.topLevel, name, value)
			}
			put(t.any, name, value)
		}
		for _, m := range assignment.FindAllStringSubmatchIndex(f.text, -1) {
			add(m)
		}
		if isJS(f.ext) {
			for _, m := range property.FindAllStringSubmatchIndex(f.text, -1) {
				add(m)
			}
		}
	}
	s.constants = t
	return t
}

// only returns the one value of a set, or "" when there is not exactly one.
func only(values map[string]bool) string {
	if len(values) != 1 {
		return ""
	}
	for v := range values {
		return v
	}
	return ""
}

// constant resolves a constant of the app's own, used at at in f, to its
// value. A constant of a type (Events.LEVEL_UP) is looked for in that
// type. One by its name alone (LEVEL_UP, kLevelUp) is looked for in the
// containers around the use, then in the one it is imported from, then
// outside any container, then anywhere; in each, it must be set to exactly
// one string.
func (s *analyticsScan) constant(expr string, f *sourceFile, at int) string {
	expr = strings.TrimSuffix(expr, ".rawValue")
	if !constantLike.MatchString(expr) {
		return ""
	}
	parts := strings.Split(expr, ".")
	last := parts[len(parts)-1]
	if len(parts) > 1 && unicode.IsUpper(rune(parts[len(parts)-2][0])) {
		return only(s.table().qualified[parts[len(parts)-2]+"."+last])
	}
	if !upperSnake.MatchString(last) && !kConstant.MatchString(last) {
		return ""
	}
	t := s.table()
	for _, in := range f.enclosing(at) {
		if values := t.qualified[in.name+"."+last]; len(values) > 0 {
			return only(values)
		}
	}
	for _, imp := range f.imports() {
		if segments := strings.Split(imp, "."); len(segments) > 2 && segments[len(segments)-1] == last {
			return only(t.qualified[segments[len(segments)-2]+"."+last])
		}
	}
	if values := t.topLevel[last]; len(values) > 0 {
		return only(values)
	}
	return only(t.any[last])
}

// sdkConstant reads an expression as one of the analytics SDKs' own
// constants, the SDK the call is to first: the name it stands for, when its
// value is known, and whether it is an SDK's at all. One that is is never
// taken for one of the app's.
func sdkConstant(sdk *analyticsSDK, expr string, f *sourceFile) (string, bool) {
	if sdk.constant != nil {
		if name, owned := sdk.constant(expr, f); owned {
			return name, true
		}
	}
	for i := range analyticsSDKs {
		if other := &analyticsSDKs[i]; other != sdk && other.constant != nil {
			if name, owned := other.constant(expr, f); owned {
				return name, true
			}
		}
	}
	return "", false
}

func apple(ext string) bool { return ext == ".swift" || ext == ".m" || ext == ".mm" }

func jvm(ext string) bool { return ext == ".kt" || ext == ".java" }

// firebaseEvents are Firebase Analytics' own events, by the name of their
// constant on iOS (AnalyticsEventLevelUp, kFIREventLevelUp) and in Unity
// (FirebaseAnalytics.EventLevelUp), as the SDK's headers define them. On
// Android, FirebaseAnalytics.Event.LEVEL_UP, each is its value in capitals,
// and in_app_purchase has none.
var firebaseEvents = map[string]string{
	"AdImpression": "ad_impression", "AddPaymentInfo": "add_payment_info", "AddShippingInfo": "add_shipping_info", "AddToCart": "add_to_cart",
	"AddToWishlist": "add_to_wishlist", "AppOpen": "app_open", "BeginCheckout": "begin_checkout", "CampaignDetails": "campaign_details",
	"EarnVirtualCurrency": "earn_virtual_currency", "GenerateLead": "generate_lead", "InAppPurchase": "in_app_purchase", "JoinGroup": "join_group",
	"LevelEnd": "level_end", "LevelStart": "level_start", "LevelUp": "level_up", "Login": "login", "PostScore": "post_score", "Purchase": "purchase",
	"Refund": "refund", "RemoveFromCart": "remove_from_cart", "ScreenView": "screen_view", "Search": "search", "SelectContent": "select_content",
	"SelectItem": "select_item", "SelectPromotion": "select_promotion", "Share": "share", "SignUp": "sign_up", "SpendVirtualCurrency": "spend_virtual_currency",
	"TutorialBegin": "tutorial_begin", "TutorialComplete": "tutorial_complete", "UnlockAchievement": "unlock_achievement", "ViewCart": "view_cart",
	"ViewItem": "view_item", "ViewItemList": "view_item_list", "ViewPromotion": "view_promotion", "ViewSearchResults": "view_search_results",
}

// firebaseAndroidEvents are FirebaseAnalytics.Event's constants.
var firebaseAndroidEvents = func() map[string]string {
	out := map[string]string{}
	for constant, value := range firebaseEvents {
		if constant != "InAppPurchase" {
			out[strings.ToUpper(value)] = value
		}
	}
	return out
}()

// firebaseConveniences are the Firebase methods that send one of its
// recommended events, such as logLevelUp for level_up.
func firebaseConveniences() map[string]string {
	out := map[string]string{}
	for constant, value := range firebaseEvents {
		if constant != "InAppPurchase" {
			out["log"+constant] = value
		}
	}
	return out
}

var (
	firebaseAndroidConstant = regexp.MustCompile(`^((?:com\.google\.firebase\.analytics\.)?FirebaseAnalytics\s*\.\s*)?Event\s*\.\s*(\w+)$`)
	firebaseAppleConstant   = regexp.MustCompile(`^(?:AnalyticsEvent|kFIREvent)(\w+)$`)
	firebaseUnityConstant   = regexp.MustCompile(`^(?:Firebase\s*\.\s*Analytics\s*\.\s*)?FirebaseAnalytics\s*\.\s*Event(\w+)$`)
	firebaseEventImport     = regexp.MustCompile(`(?m)^[ \t]*import\s+com\.google\.firebase\.analytics\.FirebaseAnalytics\.(?:Event|\*)\s*;?[ \t]*$`)
)

// firebaseConstant reads FirebaseAnalytics.Event.LEVEL_UP (or Event.LEVEL_UP
// where Firebase's Event is imported), AnalyticsEventLevelUp and
// FirebaseAnalytics.EventLevelUp, all level_up.
func firebaseConstant(expr string, f *sourceFile) (string, bool) {
	switch {
	case jvm(f.ext):
		m := firebaseAndroidConstant.FindStringSubmatch(expr)
		if m == nil || m[1] == "" && !firebaseEventImported(f) {
			return "", false
		}
		return firebaseAndroidEvents[m[2]], true
	case apple(f.ext):
		if m := firebaseAppleConstant.FindStringSubmatch(expr); m != nil && firebaseEvents[m[1]] != "" {
			return firebaseEvents[m[1]], true
		}
	case f.ext == ".cs":
		if m := firebaseUnityConstant.FindStringSubmatch(expr); m != nil {
			return firebaseEvents[m[1]], true
		}
	}
	return "", false
}

// firebaseEventImported reports whether Event, in a Kotlin or Java file, is
// Firebase's: imported, and not a class of the file's own.
func firebaseEventImported(f *sourceFile) bool {
	if !firebaseEventImport.MatchString(f.text) {
		return false
	}
	for _, s := range f.scopes() {
		if s.name == "Event" {
			return false
		}
	}
	return true
}

// AppsFlyer's event constants, as its SDKs define them: AFInAppEventType
// on Android, AFEvent… on iOS, AFInAppEvents in Unity. Their names do not
// always spell their values (ADD_TO_WISH_LIST is af_add_to_wishlist).
var (
	appsFlyerAndroidEvents = map[string]string{
		"LEVEL_ACHIEVED": "af_level_achieved", "ADD_PAYMENT_INFO": "af_add_payment_info", "ADD_TO_CART": "af_add_to_cart",
		"ADD_TO_WISH_LIST": "af_add_to_wishlist", "COMPLETE_REGISTRATION": "af_complete_registration", "TUTORIAL_COMPLETION": "af_tutorial_completion",
		"INITIATED_CHECKOUT": "af_initiated_checkout", "PURCHASE": "af_purchase", "RATE": "af_rate", "SEARCH": "af_search",
		"SPENT_CREDIT": "af_spent_credits", "ACHIEVEMENT_UNLOCKED": "af_achievement_unlocked", "CONTENT_VIEW": "af_content_view",
		"TRAVEL_BOOKING": "af_travel_booking", "SHARE": "af_share", "INVITE": "af_invite", "LOGIN": "af_login", "RE_ENGAGE": "af_re_engage",
		"UPDATE": "af_update", "OPENED_FROM_PUSH_NOTIFICATION": "af_opened_from_push_notification", "LOCATION_CHANGED": "af_location_changed",
		"LOCATION_COORDINATES": "af_location_coordinates", "ORDER_ID": "af_order_id", "CUSTOMER_SEGMENT": "af_customer_segment",
		"LIST_VIEW": "af_list_view", "SUBSCRIBE": "af_subscribe", "START_TRIAL": "af_start_trial", "AD_CLICK": "af_ad_click", "AD_VIEW": "af_ad_view",
	}
	appsFlyerUnityEvents = map[string]string{
		"LEVEL_ACHIEVED": "af_level_achieved", "ADD_PAYMENT_INFO": "af_add_payment_info", "ADD_TO_CART": "af_add_to_cart",
		"ADD_TO_WISH_LIST": "af_add_to_wishlist", "COMPLETE_REGISTRATION": "af_complete_registration", "TUTORIAL_COMPLETION": "af_tutorial_completion",
		"INITIATED_CHECKOUT": "af_initiated_checkout", "PURCHASE": "af_purchase", "RATE": "af_rate", "SEARCH": "af_search",
		"SPENT_CREDIT": "af_spent_credits", "ACHIEVEMENT_UNLOCKED": "af_achievement_unlocked", "CONTENT_VIEW": "af_content_view",
		"TRAVEL_BOOKING": "af_travel_booking", "SHARE": "af_share", "INVITE": "af_invite", "LOGIN": "af_login", "RE_ENGAGE": "af_re_engage",
		"UPDATE": "af_update", "OPENED_FROM_PUSH_NOTIFICATION": "af_opened_from_push_notification", "LOCATION_CHANGED": "af_location_changed",
		"LOCATION_COORDINATES": "af_location_coordinates", "ORDER_ID": "af_order_id",
	}
	appsFlyerAppleEvents = map[string]string{
		"LevelAchieved": "af_level_achieved", "AddPaymentInfo": "af_add_payment_info", "AddToCart": "af_add_to_cart", "AddToWishlist": "af_add_to_wishlist",
		"CompleteRegistration": "af_complete_registration", "Tutorial_completion": "af_tutorial_completion", "InitiatedCheckout": "af_initiated_checkout",
		"Purchase": "af_purchase", "Rate": "af_rate", "Search": "af_search", "SpentCredits": "af_spent_credits", "AchievementUnlocked": "af_achievement_unlocked",
		"ContentView": "af_content_view", "ListView": "af_list_view", "TravelBooking": "af_travel_booking", "Share": "af_share", "Invite": "af_invite",
		"Login": "af_login", "ReEngage": "af_re_engage", "Update": "af_update", "OpenedFromPushNotification": "af_opened_from_push_notification",
		"Location": "af_location_coordinates", "CustomerSegment": "af_customer_segment", "Subscribe": "af_subscribe", "StartTrial": "af_start_trial",
		"AdClick": "af_ad_click", "AdView": "af_ad_view",
	}
	appsFlyerAndroidConstant = regexp.MustCompile(`^(?:com\.appsflyer\.)?AFInAppEventType\s*\.\s*(\w+)$`)
	appsFlyerAppleConstant   = regexp.MustCompile(`^AFEvent(\w+)$`)
	appsFlyerUnityConstant   = regexp.MustCompile(`^(?:AppsFlyerSDK\s*\.\s*)?AFInAppEvents\s*\.\s*(\w+)$`)
)

// appsFlyerConstant reads AFInAppEventType.LEVEL_ACHIEVED, AFEventLevelAchieved
// and AFInAppEvents.LEVEL_ACHIEVED, all af_level_achieved.
func appsFlyerConstant(expr string, f *sourceFile) (string, bool) {
	switch {
	case jvm(f.ext):
		if m := appsFlyerAndroidConstant.FindStringSubmatch(expr); m != nil {
			return appsFlyerAndroidEvents[m[1]], true
		}
	case apple(f.ext):
		if m := appsFlyerAppleConstant.FindStringSubmatch(expr); m != nil && appsFlyerAppleEvents[m[1]] != "" {
			return appsFlyerAppleEvents[m[1]], true
		}
	case f.ext == ".cs":
		if m := appsFlyerUnityConstant.FindStringSubmatch(expr); m != nil {
			return appsFlyerUnityEvents[m[1]], true
		}
	}
	return "", false
}

// facebookAndroidEvents are AppEventsConstants' event names on Android.
var facebookAndroidEvents = map[string]string{
	"EVENT_NAME_ACTIVATED_APP": "fb_mobile_activate_app", "EVENT_NAME_DEACTIVATED_APP": "fb_mobile_deactivate_app",
	"EVENT_NAME_SESSION_INTERRUPTIONS": "fb_mobile_app_interruptions", "EVENT_NAME_TIME_BETWEEN_SESSIONS": "fb_mobile_time_between_sessions",
	"EVENT_NAME_COMPLETED_REGISTRATION": "fb_mobile_complete_registration", "EVENT_NAME_VIEWED_CONTENT": "fb_mobile_content_view",
	"EVENT_NAME_SEARCHED": "fb_mobile_search", "EVENT_NAME_RATED": "fb_mobile_rate", "EVENT_NAME_COMPLETED_TUTORIAL": "fb_mobile_tutorial_completion",
	"EVENT_NAME_PUSH_TOKEN_OBTAINED": "fb_mobile_obtain_push_token", "EVENT_NAME_ADDED_TO_CART": "fb_mobile_add_to_cart",
	"EVENT_NAME_ADDED_TO_WISHLIST": "fb_mobile_add_to_wishlist", "EVENT_NAME_INITIATED_CHECKOUT": "fb_mobile_initiated_checkout",
	"EVENT_NAME_ADDED_PAYMENT_INFO": "fb_mobile_add_payment_info", "EVENT_NAME_PURCHASED": "fb_mobile_purchase",
	"EVENT_NAME_ACHIEVED_LEVEL": "fb_mobile_level_achieved", "EVENT_NAME_UNLOCKED_ACHIEVEMENT": "fb_mobile_achievement_unlocked",
	"EVENT_NAME_SPENT_CREDITS": "fb_mobile_spent_credits", "EVENT_NAME_CONTACT": "Contact", "EVENT_NAME_CUSTOMIZE_PRODUCT": "CustomizeProduct",
	"EVENT_NAME_DONATE": "Donate", "EVENT_NAME_FIND_LOCATION": "FindLocation", "EVENT_NAME_SCHEDULE": "Schedule", "EVENT_NAME_START_TRIAL": "StartTrial",
	"EVENT_NAME_SUBMIT_APPLICATION": "SubmitApplication", "EVENT_NAME_SUBSCRIBE": "Subscribe", "EVENT_NAME_AD_IMPRESSION": "AdImpression",
	"EVENT_NAME_AD_CLICK": "AdClick",
}

var (
	facebookAndroidConstant = regexp.MustCompile(`^(?:com\.facebook\.appevents\.)?AppEventsConstants\s*\.\s*(\w+)$`)
	// Elsewhere, Facebook's constants are named, not read: AppEvents.Name
	// in Swift, FBSDKAppEventName… in Objective-C, AppEventName in Unity.
	facebookOtherConstant = regexp.MustCompile(`^(?:AppEvents\s*\.\s*Name\s*\.\s*\w+|FBSDKAppEventName\w+|AppEventName\s*\.\s*\w+)$`)
)

// facebookConstant reads AppEventsConstants.EVENT_NAME_PURCHASED,
// fb_mobile_purchase.
func facebookConstant(expr string, f *sourceFile) (string, bool) {
	if jvm(f.ext) {
		if m := facebookAndroidConstant.FindStringSubmatch(expr); m != nil {
			return facebookAndroidEvents[m[1]], true
		}
		return "", false
	}
	return "", facebookOtherConstant.MatchString(expr)
}
