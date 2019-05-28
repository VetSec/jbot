/*
 * This is a HipChat and Slack version of the 'jbot'
 * IRC bot, originally developed at Yahoo! in 2007.
 * This variant was created as a rewrite in Go for
 * HipChat in July 2016 by Jan Schaumann (@jschauma
 * / jschauma@netmeister.org); support for Slack was
 * added some time in early 2017.  Many thanks to
 * Yahoo for letting me play around with nonsense like
 * this.
 *
 * You should be able to run the bot by populating a
 * configuration file with suitable values.  The
 * following configuration values are required:
 *
 * For HipChat:
 *   hcPassword    = the HipChat password of the bot user
 *     OR
 *   hcOauthToken  = the HipChat Oauth token for the bot user
 *   hcService     = the HipChat company prefix, e.g. <foo>.hipchat.com
 *   hcJabberID    = the HipChat / JabberID of the bot user
 *   fullName      = how the bot presents itself
 *   mentionName   = to which name the bot responds to
 *
 * For Slack:
 *   slackService  = the Slack service name, e.g. <foo>.slack.com
 *   slackToken    = the authentication token for your bot
 *
 * This bot has a bunch of features that are company
 * internal; those features have been removed from
 * this public version.
 *
 * Some day this should be extended into a pluggable
 * bot, so that internal code can more easily be kept
 * apart, I suppose.  Pull requests welcome etc.
 */

/*
 * This code is in the public domain.  Knock yourself
 * out.  If it's not inconvenient, tell people where
 * you got it.  If we meet some day and you think this
 * code is worth it, you can buy me a beer in return.
 */

package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

import (
	"github.com/daneharrigan/hipchat"
	"github.com/nlopes/slack"
)

const EXIT_FAILURE = 1
const EXIT_SUCCESS = 0

const PROGNAME = "jbot"
const VERSION = "3.0"

const DEFAULT_THROTTLE = 1800
const PERIODICS = 60

/* Periodics are run PERIODICS * Seconds;
 * Intervals are run every I * PERIODICS * Seconds */
const CVE_FEED_UPDATE_INTERVAL = 10
const SLACK_LIVE_CHECK = 30
const SLACK_CHANNEL_UPDATE_INTERVAL = 30

/* API docs say 4000 chars, but experimentation
 * suggests we need some buffer room. */
const SLACK_MAX_LENGTH = 3500

var LAST_SLACK_MESSAGE_TIME time.Time

var SLACK_UNLINK_RE1 = regexp.MustCompile("(<https?://([^|]+)\\|([^>]+)>)")
var SLACK_UNLINK_RE2 = regexp.MustCompile("<(https?://[^>]+)>")

var CONFIG = map[string]string{
	"botOwner":             "",
	"byUser":               "",
	"byPassword":           "",
	"channelsFile":         "/var/tmp/jbot.channels",
	"countersFile":         "/var/tmp/jbot.counters",
	"configFile":           "/etc/jbot.conf",
	"debug":                "no",
	"emailDomain":          "",
	"fullName":             "",
	"giphyApiKey":          "",
	"hcControlChannel":     "",
	"hcJabberID":           "",
	"hcOauthToken":         "",
	"hcPassword":           "",
	"hcService":            "",
	"jiraPassword":         "",
	"jiraUser":             "",
	"mentionName":          "",
	"openweathermapApiKey": "",
	"opsgenieApiKey":       "",
	"slackID":              "",
	"slackService":         "",
	"slackToken":           "",
	"SMTP":                 "",
	"timezonedbApiKey":     "",
}

var SECRETS = []string{
	"byPassword",
	"hcOauthToken",
	"giphyApiKey",
	"opsgenieApiKey",
	"slackToken",
}

var HIPCHAT_CLIENT *hipchat.Client
var HIPCHAT_ROOMS = map[string]*hipchat.Room{}
var HIPCHAT_ROSTER = map[string]*hipchat.User{}

var SLACK_CLIENT *slack.Client
var SLACK_RTM *slack.RTM

var SLACK_CHANNELS = map[string]slack.Channel{}
var CHANNELS = map[string]*Channel{}
var COMMANDS = map[string]*Command{}
var COUNTERS = map[string]map[string]int{
	"commands": map[string]int{},
	"curses":   map[string]int{},
	"cursers":  map[string]int{},
	"insulted": map[string]int{},
	"praised":  map[string]int{},
	"replies":  map[string]int{},
	"thanked":  map[string]int{},
	"yubifail": map[string]int{},
}

var TOGGLES = map[string]bool{
	"atnoyance":   true,
	"chatter":     true,
	"corpbs":      true,
	"python":      true,
	"trivia":      true,
	"shakespeare": true,
	"schneier":    true,
}

var URLS = map[string]string{
	"insults": "http://localhost/quips",
	"jbot":    "https://github.com/jschauma/jbot/",
	"jira":    "https://<yourjiraurlhere>",
	"parrots": "http://localhost/parrots",
	"praise":  "http://localhost/praise",
	"pwgen":   "https://www.netmeister.org/pwgen/",
	"speb":    "http://localhost/speb",
	"trivia":  "http://localhost/trivia",
}

var COOKIES []*http.Cookie
var VERBOSITY int

type PhishCount struct {
	Count int
	Total int
	First time.Time
	Last  time.Time
}

const PHISH_MAX = 5
const PHISH_TIME = 1200

type Channel struct {
	CVEs         map[string]CVEItem
	Inviter      string
	Id           string
	Name         string
	Toggles      map[string]bool
	Throttles    map[string]time.Time
	Type         string
	HipChatUsers map[hipchat.User]UserInfo
	SlackUsers   map[string]UserInfo
	Settings     map[string]string
	Phishy       *PhishCount
}

type CommandFunc func(Recipient, string, string) string

type Command struct {
	Call    CommandFunc
	Help    string
	How     string
	Usage   string
	Aliases []string
}

type UserInfo struct {
	Count      int
	Curses     int
	CurseWords map[string]int
	Id         string
	Seen       string
	Yubifail   int
}

/*
 * ChatType    = hipchat|slack
 * Id          = 12345_98765@conf.hipchat.com | C62HJV9F0
 * MentionName = JohnDoe
 * Name        = John Doe
 * ReplyTo     = 98765 | U3GNF8QGJ
 *
 * To handle both HipChat and Slack, we overload the
 * fields a bit: for Slack, "ReplyTo" is the channel.
 */
type Recipient struct {
	ChatType    string
	Id          string
	MentionName string
	Name        string
	ReplyTo     string
}

/*
 * Commands
 */

func cmdAsn(r Recipient, chName, args string) (result string) {
	input := strings.Split(args, " ")
	if len(args) < 1 || len(input) != 1 {
		result = "Usage: " + COMMANDS["asn"].Usage
		return
	}

	arg := input[0]
	number_re := regexp.MustCompile(`(?i)^(asn?)?([0-9]+)$`)
	m := number_re.FindStringSubmatch(arg)
	if len(m) > 0 {
		arg = "AS" + m[2]
	} else if net.ParseIP(arg) == nil {
		arg = fqdn(arg)
		addrs, err := net.LookupHost(arg)
		if err != nil {
			result = "Not a valid ASN, IP or hostname."
			return
		}
		arg = addrs[0]
	}

	command := strings.Fields(COMMANDS["asn"].How)
	command = append(command, arg)

	data, _ := runCommand(command...)
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		result = "No ASN information found."
	} else {
		result = lines[len(lines)-2]
	}

	return
}

func cmdBacon(r Recipient, chName, args string) (result string) {
	pic := false
	query := "bacon"
	if len(args) > 0 {
		query += " " + args
		pic = true
	}

	rand.Seed(time.Now().UnixNano())
	if pic || rand.Intn(4) == 0 {
		result = cmdImage(r, chName, query)
	} else {
		data := getURLContents("https://baconipsum.com/?paras=1&type=all-meat", nil)
		bacon_re := regexp.MustCompile(`anyipsum-output">(.*?\.)`)
		for _, line := range strings.Split(string(data), "\n") {
			if m := bacon_re.FindStringSubmatch(line); len(m) > 0 {
				result = dehtmlify(m[1])
				break
			}
		}
	}

	if len(result) < 1 {
		result = "Ugh, I'm afraid I'm all out of bacon right now."
	}

	return
}

func cmdBeer(r Recipient, chName, args string) (result string) {
	bType := "search"
	theUrl := fmt.Sprintf("%ssearch/?qt=beer&q=", COMMANDS["beer"].How)
	if len(args) < 1 {
		bType = "top"
		theUrl = fmt.Sprintf("%slists/top/", COMMANDS["beer"].How)
	}

	if args == "me" {
		args = r.MentionName
	}

	theUrl += url.QueryEscape(args)
	data := getURLContents(theUrl, nil)

	type Beer struct {
		Abv      string
		BeerType string
		Brewery  string
		Name     string
		Rating   string
		Url      string
	}

	var beer Beer

	beer_re := regexp.MustCompile(`<a href="/(beer/profile/[0-9]+/[0-9]+/)"><span[^>]+>([^<]+)</span></a><br><span[^>]+><a href="/beer/profile/[0-9]+/">([^<]+)</a>`)
	top_re := regexp.MustCompile(`<a href="/(beer/profile/[0-9]+/[0-9]+/)"><b>([^<]+)</b></a><span[^>]+><br><a href="/beer/profile/[0-9]+/">([^<]+)</a><br><a href="/beer/styles/[0-9]+/">([^<]+)</a> \| ([0-9.]+)%</span></td><td.+><b>([0-9.]+)</b>`)

	for _, line := range strings.Split(string(data), "\n") {
		if bType == "search" {
			if m := beer_re.FindStringSubmatch(line); len(m) > 0 {
				beer = Beer{"", "", m[3], m[2], "", m[1]}
				theUrl = fmt.Sprintf("%s%s", COMMANDS["beer"].How, m[1])
				data := getURLContents(theUrl, nil)
				style_re := regexp.MustCompile(`<b>Style:</b> <a href=.*><b>(.*)</b></a>`)
				abv_re := regexp.MustCompile(`<b>Alcohol by volume \(ABV\):</b> (.*)`)
				next := false
				for _, l2 := range strings.Split(string(data), "\n") {
					if strings.Contains(l2, "<dt>Avg:</dt>") {
						next = true
						continue
					}
					if next {
						beer.Rating = dehtmlify(l2)
						next = false
					}
					if m := abv_re.FindStringSubmatch(l2); len(m) > 0 {
						beer.Abv = m[1]
					}
					if m := style_re.FindStringSubmatch(l2); len(m) > 0 {
						beer.BeerType = m[1]
					}
				}
				break
			}
		} else {
			if strings.HasPrefix(line, "<tr><td align=") {
				beers := []Beer{}
				for _, l2 := range strings.Split(line, "</tr>") {
					if m := top_re.FindStringSubmatch(l2); len(m) > 0 {
						b := Beer{m[5], m[4], m[3], m[2], m[6], m[1]}
						beers = append(beers, b)
					}
				}
				if len(beers) > 0 {
					rand.Seed(time.Now().UnixNano())
					beer = beers[rand.Intn(len(beers))]
				}
			}
		}
	}

	if len(beer.Name) > 0 {
		result = fmt.Sprintf("%s by %s - %s\n", beer.Name, beer.Brewery, beer.Rating)
		result += fmt.Sprintf("%s (%s)\n", beer.BeerType, beer.Abv)
		result += fmt.Sprintf("%s%s\n", COMMANDS["beer"].How, beer.Url)
	} else {
		result = fmt.Sprintf("No beer found for '%s'.", args)
	}

	return
}

func cmdBs(r Recipient, chName, args string) (result string) {

	answer := ""

	rand.Seed(time.Now().UnixNano())
	var s = []string{
		fmt.Sprintf("Well, @%s, I think you should probably", r.MentionName),
		"A better idea:",
		"Or perhaps",
		"Y'all should",
		"Here's an idea:",
		"To remain competitive, we need to",
		"In order to succeed, we must",
		"Team! Let's",
		"Uhm, lemme think for a second there. How about:",
	}

	answer = s[rand.Intn(len(s))] + " "

	var adverbs = []string{
		"appropriately",
		"assertively",
		"authoritatively",
		"collaboratively",
		"compellingly",
		"competently",
		"completely",
		"continually",
		"conveniently",
		"credibly",
		"distinctively",
		"dramatically",
		"dynamically",
		"efficiently",
		"energistically",
		"enthusiastically",
		"fungibly",
		"globally",
		"holisticly",
		"interactively",
		"intrinsically",
		"monotonectally",
		"objectively",
		"phosfluorescently",
		"proactively",
		"professionally",
		"progressively",
		"quickly",
		"rapidiously",
		"seamlessly",
		"synergistically",
		"uniquely",
	}

	var verbs = []string{
		"actualize",
		"administrate",
		"aggregate",
		"architect",
		"benchmark",
		"brand",
		"build",
		"cloudify",
		"communicate",
		"conceptualize",
		"coordinate",
		"create",
		"cultivate",
		"customize",
		"deliver",
		"deploy",
		"develop",
		"disintermediate",
		"disseminate",
		"drive",
		"e-enable",
		"embrace",
		"empower",
		"enable",
		"engage",
		"engineer",
		"enhance",
		"envisioneer",
		"evisculate",
		"evolve",
		"expedite",
		"exploit",
		"extend",
		"fabricate",
		"facilitate",
		"fashion",
		"formulate",
		"foster",
		"generate",
		"grow",
		"harness",
		"impact",
		"implement",
		"incentivize",
		"incubate",
		"initiate",
		"innovate",
		"integrate",
		"iterate",
		"leverage existing",
		"leverage other's",
		"maintain",
		"matrix",
		"maximize",
		"mesh",
		"monetize",
		"morph",
		"myocardinate",
		"negotiate",
		"network",
		"optimize",
		"orchestrate",
		"paralleltask",
		"plagiarize",
		"pontificate",
		"predominate",
		"procrastinate",
		"productivate",
		"productize",
		"promote",
		"provide access to",
		"pursue",
		"re-engineer",
		"recaptiualize",
		"reconceptualize",
		"redefine",
		"reintermediate",
		"reinvent",
		"repurpose",
		"restore",
		"revolutionize",
		"right-shore",
		"scale",
		"seize",
		"simplify",
		"strategize",
		"streamline",
		"supply",
		"syndicate",
		"synergize",
		"synthesize",
		"target",
		"transform",
		"transition",
		"underwhelm",
		"unleash",
		"utilize",
		"visualize",
		"whiteboard",
	}

	var adjectives = []string{
		"24/365",
		"24/7",
		"B2B",
		"B2C",
		"accurate",
		"adaptive",
		"agile",
		"alternative",
		"an expanded array of",
		"backend",
		"backward-compatible",
		"best-of-breed",
		"bleeding-edge",
		"bricks-and-clicks",
		"business",
		"clicks-and-mortar",
		"client-based",
		"client-centered",
		"client-centric",
		"client-focused",
		"cloud-based",
		"cloud-centric",
		"cloud-ready",
		"cloudified",
		"collaborative",
		"compelling",
		"competitive",
		"cooperative",
		"corporate",
		"costeffective",
		"covalent",
		"cross-media",
		"cross-platform",
		"cross-unit",
		"crossfunctional",
		"customer directed",
		"customized",
		"cutting-edge",
		"distinctive",
		"distributed",
		"diverse",
		"dynamic",
		"e-business",
		"economically sound",
		"effective",
		"efficient",
		"elastic",
		"emerging",
		"empowered",
		"enabled",
		"end-to-end",
		"enterprise",
		"enterprise-wide",
		"equity invested",
		"error-free",
		"ethical",
		"excellent",
		"exceptional",
		"extensible",
		"extensive",
		"flexible",
		"focused",
		"frictionless",
		"front-end",
		"fully researched",
		"fully tested",
		"functional",
		"functionalized",
		"fungible",
		"future-proof",
		"global",
		"goal-oriented",
		"goforward",
		"granular",
		"high-payoff",
		"high-quality",
		"highly efficient",
		"high standards in",
		"holistic",
		"hyper-scale",
		"impactful",
		"inexpensive",
		"innovative",
		"installedbase",
		"integrated",
		"interactive",
		"interdependent",
		"intermandated",
		"interoperable",
		"intuitive",
		"justintime",
		"leading-edge",
		"leveraged",
		"long-termhigh-impact",
		"low-riskhigh-yield",
		"magnetic",
		"maintainable",
		"market-driven",
		"market positioning",
		"mission-critical",
		"multidisciplinary",
		"multifunctional",
		"multimedia based",
		"next-generation",
		"on-demand",
		"one-to-one",
		"open-source",
		"optimal",
		"orthogonal",
		"out-of-the-box",
		"pandemic",
		"parallel",
		"performancebased",
		"plug-and-play",
		"premier",
		"premium",
		"principle-centered",
		"proactive",
		"process-centric",
		"professional",
		"progressive",
		"prospective",
		"quality",
		"real-time",
		"reliable",
		"resource-leveling",
		"resource-maximizing",
		"resource-sucking",
		"revolutionary",
		"robust",
		"scalable",
		"seamless",
		"stand-alone",
		"standardized",
		"standardscompliant",
		"stateoftheart",
		"sticky",
		"strategic",
		"superior",
		"sustainable",
		"synergistic",
		"tactical",
		"teambuilding",
		"teamdriven",
		"technicallysound",
		"timely",
		"top-line",
		"transparent",
		"turnkey",
		"ubiquitous",
		"unique",
		"user-centric",
		"userfriendly",
		"value-added",
		"vertical",
		"viral",
		"virtual",
		"visionary",
		"web-enabled",
		"wireless",
		"world-class",
		"worldwide",
	}

	var nouns = []string{
		"'outsidethebox' thinking",
		"IoT",
		"ROI",
		"actionitems",
		"alignments",
		"applications",
		"architectures",
		"bandwidth",
		"benefits",
		"best practices",
		"blockchain",
		"catalysts for change",
		"channels",
		"clouds",
		"collaborationandidea-sharing",
		"communities",
		"content",
		"convergence",
		"core competencies",
		"crypto currencies",
		"customer service",
		"data",
		"deliverables",
		"e-business",
		"e-commerce",
		"e-markets",
		"e-services",
		"e-tailers",
		"experiences",
		"expertise",
		"functionalities",
		"fungibility",
		"growth strategies",
		"human capital",
		"ideas",
		"imperatives",
		"infomediaries",
		"information",
		"infrastructures",
		"initiatives",
		"innovation",
		"intellectual capital",
		"interfaces",
		"internal or 'organic' sources",
		"leadership",
		"leadership skills",
		"manufactured products",
		"markets",
		"materials",
		"meta-services",
		"methodologies",
		"methods of empowerment",
		"metrics",
		"mindshare",
		"models",
		"networks",
		"niche markets",
		"niches",
		"nosql",
		"opportunities",
		"outsourcing",
		"paradigms",
		"partnerships",
		"platforms",
		"portals",
		"potentialities",
		"processes",
		"process improvements",
		"products",
		"quality vectors",
		"relationships",
		"resources",
		"results",
		"scenarios",
		"schemas",
		"scrums",
		"services",
		"solutions",
		"sources",
		"sprints",
		"storage",
		"strategic theme areas",
		"supplychains",
		"synergy",
		"systems",
		"technologies",
		"technology",
		"testing procedures",
		"total linkage",
		"users",
		"value",
		"virtualization",
		"vortals",
		"web-readiness",
		"webservices",
		"wins",
	}

	answer += adverbs[rand.Intn(len(adverbs))] + " " +
		verbs[rand.Intn(len(verbs))] + " " +
		adjectives[rand.Intn(len(adjectives))] + " " +
		nouns[rand.Intn(len(nouns))] + "!"

	result = answer

	return
}

func cmdCert(r Recipient, chName, args string) (result string) {
	names := strings.Split(args, " ")
	if len(args) < 1 || len(names) > 3 {
		result = "Usage: " + COMMANDS["cert"].Usage
		return
	}

	names[0] = strings.TrimPrefix(names[0], "https://")
	names[0] = strings.TrimSuffix(names[0], "/")

	ipv6 := false
	ipv6_re := regexp.MustCompile(`(?i)^\[?([a-f0-9:]+)\]?(:[0-9]+)?$`)
	m := ipv6_re.FindStringSubmatch(names[0])
	if len(m) > 0 {
		ipv6 = true
	} else {
		name_port_re := regexp.MustCompile(`(?i)^([^: ]+)(:[0-9]+)?$`)
		m = name_port_re.FindStringSubmatch(names[0])
		if len(m) < 1 {
			result = "Invalid argument. Try an FQDN followed by an optional port.\n"
			result += "For example: www.yahoo.com:443\n"
			return
		}
	}

	if len(m[2]) < 1 {
		if ipv6 {
			names[0] = fmt.Sprintf("[%s]:443", names[0])
		} else {
			names[0] += ":443"
		}
	}

	/* This call is intended to show information
	 * about the cert, even if the cert is not
	 * valid, so here we actually ignore cert
	 * errors for once. */
	config := &tls.Config{InsecureSkipVerify: true}

	chain := false
	if len(names) > 1 {
		if names[1] == "all" || names[1] == "chain" {
			chain = true
		} else {
			config = &tls.Config{InsecureSkipVerify: true, ServerName: names[1]}
		}

		if len(names) == 3 {
			chain = true
		}
	}

	conn, err := tls.Dial("tcp", names[0], config)
	if err != nil {
		result = fmt.Sprintf("Unable to make a TLS connection to '%s'.\n", names[0])
		return
	}

	for n, c := range conn.ConnectionState().PeerCertificates {
		if chain {
			result += fmt.Sprintf("Certificate %d:\n", n)
		}
		result += "```\n"
		result += fmt.Sprintf("Serial Number: ")
		hex := fmt.Sprintf("%x", c.SerialNumber)
		if len(hex)%2 != 0 {
			hex = "0" + hex
		}
		for i, b := range hex {
			if i > 0 && i%2 == 0 {
				result += fmt.Sprintf(":")
			}
			result += fmt.Sprintf("%s", string(b))
		}
		result += fmt.Sprintf("\n")

		result += fmt.Sprintf("Subject      : %s\n", c.Subject)
		result += fmt.Sprintf("Issuer       : %s\n", c.Issuer)

		if c.Subject.String() == c.Issuer.String() {
			result += "Note         : SELF-SIGNED\n"
		}

		result += "Validity     : "
		now := time.Now()
		if now.Before(c.NotBefore) {
			result += "NOT YET"
		} else if now.After(c.NotAfter) {
			result += "EXPIRED"
		}
		result += "\n"

		result += fmt.Sprintf("   Not Before: %s\n", c.NotBefore)
		result += fmt.Sprintf("   Not After : %s\n", c.NotAfter)
		if len(c.DNSNames) > 0 {
			result += fmt.Sprintf("%d SANs:\n%s\n", len(c.DNSNames), strings.Join(c.DNSNames, " "))
		}
		result += "```\n"

		if !chain {
			break
		}
	}

	return
}

func cmdChannels(r Recipient, chName, args string) (result string) {
	var hipChatChannels []string
	var slackChannels []string

	if len(CHANNELS) == 0 {
		result = "I'm not currently in any channels."
	} else if len(CHANNELS) == 1 {
		result = "I'm only here right now: "
	}

	for ch, chInfo := range CHANNELS {
		if chInfo.Type == "hipchat" {
			hipChatChannels = append(hipChatChannels, ch)
		} else if chInfo.Type == "slack" {
			slackChannels = append(slackChannels, chInfo.Name)
		}
	}
	sort.Strings(hipChatChannels)
	sort.Strings(slackChannels)
	if len(hipChatChannels) > 0 {
		result = fmt.Sprintf("I'm in the following %d HipChat channels:\n", len(hipChatChannels))
		result += strings.Join(hipChatChannels, ", ") + "\n"
	}
	if len(slackChannels) > 0 {
		result += fmt.Sprintf("I'm in the following %d Slack channels:\n", len(slackChannels))
		result += strings.Join(slackChannels, ", ")
	}
	return
}

func cmdCidr(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["cidr"].Usage
		return
	}

	/* We're lazy here, but good enough. */
	if !strings.Contains(args, "/") {
		if strings.Contains(args, ":") {
			args += "/128"
		} else {
			args += "/32"
		}
	}
	ip, ipnet, err := net.ParseCIDR(args)
	if err != nil {
		result = fmt.Sprintf("'%s' does not look like a valid CIDR to me.", args)
		return
	}

	result = fmt.Sprintf("Host address: %s\n", ip.String())
	ones, bits := ipnet.Mask.Size()
	diff := bits - ones
	num := math.Exp2(float64(diff))
	first := ip.Mask(ipnet.Mask)

	var last uint32
	isv4 := ip.To4()

	if isv4 != nil {
		ipint := big.NewInt(0)
		ipint.SetBytes(first.To4())
		decip := ipint.Int64()
		last = uint32(decip + int64(num) - 1)

		result += fmt.Sprintf("Host address (decimal): %d\n", decip)
		result += fmt.Sprintf("Host address (hex): %X\n", ipint.Int64())

		if len(ipnet.Mask) == 4 {
			result += fmt.Sprintf("Network mask (decimal): %d.%d.%d.%d\n", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3])
		}
		result += fmt.Sprintf("Network mask (hex): %s\n", ipnet.Mask)
	} else {
		result += fmt.Sprintf("Prefix length: %d\n", ones)
	}

	result += fmt.Sprintf("Addresses in network: %0.f\n", num)
	result += fmt.Sprintf("Network address: %s\n", first)
	if isv4 != nil {
		brip := make(net.IP, 4)
		binary.BigEndian.PutUint32(brip, last)
		result += fmt.Sprintf("Broadcast address: %s\n", brip)
	}

	if ip.IsGlobalUnicast() {
		result += fmt.Sprintf("Type: global unicast\n")
	}
	if ip.IsInterfaceLocalMulticast() {
		result += fmt.Sprintf("Type: interface-local multicast\n")
	}
	if ip.IsLinkLocalMulticast() {
		result += fmt.Sprintf("Type: link-local multicast\n")
	}
	if ip.IsLinkLocalUnicast() {
		result += fmt.Sprintf("Type: link-local unicast\n")
	}
	if ip.IsMulticast() {
		result += fmt.Sprintf("Type: multicast\n")
	}

	return
}

func cmdClear(r Recipient, chName, args string) (result string) {
	count := 24

	if len(args) > 0 {
		if _, err := fmt.Sscanf(args, "%d", &count); err != nil {
			result = cmdInsult(r, chName, "me")
			return
		}
	}
	if count < 1 {
		result = cmdInsult(r, chName, "me")
		return
	}

	if count > 40 {
		result = "I'm not going to clear more than 40 lines."
		return
	}

	n := 0
	rcount := count
	result = "```\n"
	for n < count {
		i := rcount
		for i > 0 {
			result += "."
			i--
		}

		result += "\n"
		if rcount == 9 {
			cowsay := cmdCowsay(r, chName, "clear")
			// strip leading "/quote "
			cowsay = cowsay[8:]
			result += " " + cowsay
			break
		} else {
			n++
			rcount--
		}
	}
	return
}

func cmdCowsay(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["cowsay"].Usage
		return
	}

	out, _ := runCommand("cowsay " + args)
	result += "```\n" + string(out) + "```\n"

	return
}

func cmdCurses(r Recipient, chName, args string) (result string) {
	result = getCountable("curses", chName, r, args)
	return
}

func cmdEightBall(r Recipient, chName, args string) (result string) {
	rand.Seed(time.Now().UnixNano())
	answers := []string{
		"It is certain.",
		"It is decidedly so.",
		"Without a doubt.",
		"Yes definitely.",
		"You may rely on it.",
		"As I see it, yes.",
		"Most likely.",
		"Outlook good.",
		"Yes.",
		"Signs point to yes.",
		"Reply hazy try again.",
		"Ask again later.",
		"Better not tell you now.",
		"Cannot predict now.",
		"Concentrate and ask again.",
		"Don't count on it.",
		"My reply is no.",
		"My sources say no.",
		"Outlook not so good.",
		"Very doubtful.",
	}
	result = answers[rand.Intn(len(answers))]
	return
}

func cmdFml(r Recipient, chName, args string) (result string) {
	if len(args) > 1 {
		result = "Usage: " + COMMANDS["fml"].Usage
		return
	}

	data := getURLContents(COMMANDS["fml"].How, nil)

	fml_re := regexp.MustCompile(`(?i)^(Today, .*FML)$`)
	for _, line := range strings.Split(string(data), "\n") {
		m := fml_re.FindStringSubmatch(line)
		if len(m) > 0 {
			result = dehtmlify(m[1])
			return
		}
	}
	return
}

func cmdFortune(r Recipient, chName, args string) (result string) {
	if len(args) > 1 {
		result = "Usage: " + COMMANDS["fortune"].Usage
		return
	}

	out, _ := runCommand("fortune -s")
	result = string(out)

	return
}

func cmdGiphy(r Recipient, chName, args string) (result string) {
	key := CONFIG["giphyApiKey"]
	if len(key) < 1 {
		result = "Sorry - no giphy API key in config file!\n"
		result += "Try '!img' instead?\n"
		result += "I know, it's not the same..."
		return
	}

	theUrl := COMMANDS["giphy"].How
	if len(args) < 1 {
		theUrl = strings.Replace(theUrl, "search", "random?", 1)
	} else {
		if args == "jbot" {
			result = "https://jbot.corp.yahoo.com/jbot.gif"
			return
		}
		theUrl += "?q=" + url.QueryEscape(args)
	}

	theUrl += "&api_key=" + url.QueryEscape(key)
	theUrl += "&rating=g&limit=30"
	data := getURLContents(theUrl, nil)

	var giphyJson map[string]interface{}
	err := json.Unmarshal(data, &giphyJson)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal giphy data: %s\n", err)
		return
	}

	if _, found := giphyJson["meta"]; !found {
		fmt.Fprintf(os.Stderr, "+++ giphy fail: %v\n", giphyJson)
		result = fmt.Sprintf("No data received from giphy!")
		return
	}

	status := giphyJson["meta"].(map[string]interface{})["status"].(float64)

	if status != 200 {
		fmt.Fprintf(os.Stderr, "+++ giphy return status %f: %v\n", status, giphyJson)
		result = fmt.Sprintf("Giphy responded with a non-200 status code!")
		return
	}

	rand.Seed(time.Now().UnixNano())
	var images map[string]interface{}
	giphyData, sOk := giphyJson["data"].([]interface{})
	if sOk {
		n := giphyJson["pagination"].(map[string]interface{})["count"].(float64)
		if n == 0 {
			result = fmt.Sprintf("No giphy results found for '%s'.", args)
			return
		}
		element := giphyData[rand.Intn(int(n))].(map[string]interface{})
		images = element["images"].(map[string]interface{})
	} else {
		giphyMap := giphyJson["data"].(map[string]interface{})
		images = giphyMap["images"].(map[string]interface{})
	}
	fixed_height := images["fixed_height"].(map[string]interface{})
	result = fixed_height["url"].(string)

	return
}

func cmdHelp(r Recipient, chName, args string) (result string) {
	if args == "all" {
		var cmds []string
		result = "These are commands I know:\n"
		for c := range COMMANDS {
			cmds = append(cmds, c)
		}
		sort.Strings(cmds)
		result += strings.Join(cmds, ", ")
	} else if len(args) < 1 {
		result = fmt.Sprintf("I know %d commands.\n"+
			"Use '!help all' to show all commands.\n"+
			"Ask me about a specific command via '!help <cmd>'.\n"+
			"If you find me annoyingly chatty, just '!toggle chatter'.\n",
			len(COMMANDS))
		result += "To ask me to leave a channel, say '!leave'.\n"
		result += "If you need any other help or have suggestions or complaints, find support in #yaybot.\n"
	} else {
		for _, cmd := range strings.Split(args, " ") {

			//alias := findCommandAlias(cmd)
			if _, found := COMMANDS[cmd]; found {
				result = fmt.Sprintf("%s: %s. Usage:\n%s",
					cmd,
					COMMANDS[cmd].Help,
					COMMANDS[cmd].Usage)
				if len(COMMANDS[cmd].Aliases) > 0 {
					result += "\nThis command can also be invoked as: '!"
					result += strings.Join(COMMANDS[cmd].Aliases, "', '!")
					result += "'."
				}
			} else {
				/* 35 to account for 'No such command...' */
				if len(cmd) >= (SLACK_MAX_LENGTH - 35) {
					result = cmdInsult(r, chName, "me")
				} else {
					result = fmt.Sprintf("No such command: %s. Try '!help'.", cmd)
				}
			}
		}
	}
	return
}

func cmdHost(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["host"].Usage
		return
	}

	out, _ := runCommand(fmt.Sprintf("host %s", args))
	result = string(out)

	return
}

func cmdHow(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["how"].Usage
		return
	}

	if _, found := COMMANDS[args]; found {
		result = COMMANDS[args].How
	} else if strings.EqualFold(args, CONFIG["mentionName"]) {
		result = URLS["jbot"]
	} else {
		rand.Seed(time.Now().UnixNano())
		result = DONTKNOW[rand.Intn(len(DONTKNOW))]
	}

	return
}

func cmdImage(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["img"].Usage
		return
	}

	theUrl := fmt.Sprintf("%s%s", COMMANDS["img"].How, url.QueryEscape(args))
	data := getURLContents(theUrl, nil)

	imgurl_re := regexp.MustCompile(`imgurl=(.*?)&`)
	for _, line := range strings.Split(string(data), "\n") {
		m := imgurl_re.FindAllStringSubmatch(line, -1)
		if len(m) > 0 {
			rand.Seed(time.Now().UnixNano())
			onePic := m[rand.Intn(len(m))]
			url, _ := url.QueryUnescape(onePic[1])
			result = "http://" + url
		}
	}

	return
}

func cmdInfo(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		args = r.ReplyTo
	}

	slack_channel_re := regexp.MustCompile(`(?i)<(#[A-Z0-9]+)\|([^>]+)>`)
	m := slack_channel_re.FindStringSubmatch(args)
	if len(m) > 0 {
		result = getChannelInfo(m[1])
		args = m[2]
	} else {
		result = getChannelInfo(args)
	}

	args = strings.ToLower(args)
	if ch, found := getChannel(r.ChatType, args); found {
		result += fmt.Sprintf("I was invited into #%s by %s.\n", ch.Name, ch.Inviter)
		result += fmt.Sprintf("These are the users I've seen in #%s:\n", ch.Name)

		var names []string

		if r.ChatType == "hipchat" {
			for u := range ch.HipChatUsers {
				names = append(names, u.MentionName)
			}
		} else if r.ChatType == "slack" {
			for u := range ch.SlackUsers {
				names = append(names, u)
			}
		}
		sort.Strings(names)
		result += strings.Join(names, ", ")

		stfu := cmdStfu(r, ch.Name, "")
		if len(stfu) > 0 {
			result += fmt.Sprintf("\nTop 10 channel chatterers for #%s:\n", ch.Name)
			result += fmt.Sprintf("%s", stfu)
		}

		toggles := cmdToggle(r, ch.Name, "")
		if len(toggles) > 0 {
			result += fmt.Sprintf("\n%s", toggles)
		}

		throttles := cmdThrottle(r, ch.Name, "")
		if len(throttles) > 0 {
			result += fmt.Sprintf("\n%s", throttles)
		}

		settings := cmdSet(r, ch.Name, "")
		if !strings.HasPrefix(settings, "There currently are no settings") {
			result += "\nThese are the channel settings:\n"
			result += settings
		}
	} else {
		result += "I'm not currently in #" + args
	}
	return
}

func cmdInsult(r Recipient, chName, args string) (result string) {
	at_mention := "<@" + CONFIG["slackID"] + ">"
	if (len(args) > 0) &&
		((strings.ToLower(args) == strings.ToLower(CONFIG["mentionName"])) ||
			(strings.ToLower(args) == "@"+strings.ToLower(CONFIG["mentionName"])) ||
			(strings.ToLower(args) == strings.ToLower(at_mention)) ||
			(args == "yourself") ||
			(args == "me")) {
		incrementCounter("insulted", r.MentionName)
		result = fmt.Sprintf("@%s: ", r.MentionName)
	}

	if (len(result) < 1) && (len(args) > 0) {
		incrementCounter("insulted", args)
		result = fmt.Sprintf("%s: ", args)
	}

	rand.Seed(time.Now().UnixNano())
	if rand.Intn(2) == 0 {
		url := URLS["insults"]
		result += randomLineFromUrl(url, false)
	} else {
		data := getURLContents(COMMANDS["insult"].How, nil)
		found := false
		insult_re := regexp.MustCompile(`^<p><font size="\+2">`)
		for _, line := range strings.Split(string(data), "\n") {
			if insult_re.MatchString(line) {
				found = true
				continue
			}
			if found {
				result += gothicText(dehtmlify(line))
				break
			}
		}
	}

	return
}

func cmdJira(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["jira"].Usage
		return
	}

	urlArgs := map[string]string{
		"basic-auth-user":     CONFIG["jiraUser"],
		"basic-auth-password": CONFIG["jiraPassword"],
	}
	ticket := strings.TrimSpace(strings.Split(args, " ")[0])
	ticket = strings.TrimPrefix(args, URLS["jira"] + "/browse/")
	jiraUrl := fmt.Sprintf("%s%s", COMMANDS["jira"].How, ticket)
	data := getURLContents(jiraUrl, urlArgs)

	var jiraJson map[string]interface{}
	err := json.Unmarshal(data, &jiraJson)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal jira data: %s\n", err)
		return
	}

	if _, found := jiraJson["fields"]; !found {
		if errmsg, found := jiraJson["errorMessages"]; found {
			result = fmt.Sprintf("Unable to fetch data for %s: %s",
				ticket, errmsg.([]interface{})[0].(string))
			return
		}
		fmt.Fprintf(os.Stderr, "+++ jira fail for %s: %v\n", ticket, jiraJson)
		result = fmt.Sprintf("No data found for ticket %s", ticket)
		return
	}

	fields := jiraJson["fields"]
	status := fields.(map[string]interface{})["status"].(map[string]interface{})["name"]
	created := fields.(map[string]interface{})["created"]
	summary := fields.(map[string]interface{})["summary"]
	reporter := fields.(map[string]interface{})["reporter"].(map[string]interface{})["name"]

	result = fmt.Sprintf("```Summary : %s\n", summary)
	result += fmt.Sprintf("Status  : %s\n", status)
	result += fmt.Sprintf("Created : %s\n", created)

	resolved := fields.(map[string]interface{})["resolutiondate"]
	if resolved != nil {
		result += fmt.Sprintf("Resolved: %s\n", resolved)
	}

	assignee := fields.(map[string]interface{})["assignee"]
	if assignee != nil {
		name := assignee.(map[string]interface{})["name"]
		result += fmt.Sprintf("Assignee: %s\n", name)
	}

	result += fmt.Sprintf("Reporter: %s```\n", reporter)
	result += fmt.Sprintf("%s/browse/%s", URLS["jira"], ticket)
	return
}

func cmdLatLong(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["latlong"].Usage
		return
	}

	client := &http.Client{}

	v := url.Values{}
	v.Add("action", "gpcm")
	v.Add("c1", args)

	latlongURL := COMMANDS["latlong"].How + "_spm4.php"
	req, err := http.NewRequest("POST", latlongURL, strings.NewReader(v.Encode()))
	if err != nil {
		result = fmt.Sprintf("Unable to create a new POST request: %s", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := client.Do(req)
	if err != nil {
		result = fmt.Sprintf("Unable to post data to %s: %s", latlongURL, err)
		return
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		result = fmt.Sprintf("Unable to read body of '%s': %s", latlongURL, err)
		return
	}

	result = string(data)
	return
}

func cmdLog(r Recipient, chName, args string) (result string) {
	var room string
	if r.ChatType == "hipchat" {
		room = r.ReplyTo
	} else if r.ChatType == "slack" {
		room = chName
	}
	if len(args) > 1 {
		room = args
	}

	roomInfo := cmdRoom(r, chName, room)

	if strings.Contains(roomInfo, "https://") {
		result = roomInfo[strings.Index(roomInfo, "https://"):]
	} else {
		result = fmt.Sprintf("No log URL found for '%s'.", r.ReplyTo)
	}
	return
}

func cmdMan(r Recipient, chName, args string) (result string) {
	query := strings.Split(args, " ")
	if len(args) < 1 || len(query) > 2 {
		result = "Usage: " + COMMANDS["man"].Usage
		return
	}

	if args == "woman" {
		rand.Seed(time.Now().UnixNano())
		replies := []string{
			"That's not very original, now is it?",
			":face_with_rolling_eyes:",
			"Good one. Never seen that before.",
			"What's next? 'make love'?",
		}
		result = replies[rand.Intn(len(replies))]
		return
	}

	var section string
	if len(query) == 2 {
		section = url.QueryEscape(query[0])
	}

	cmd := url.QueryEscape(query[len(query)-1])

	if len(section) > 0 {
		result = getManResults(section, cmd)
	} else {

		sections := []string{"1", "1p", "2", "2p", "3", "3p", "4", "4p", "5", "5p", "6", "6p", "7", "7p", "8", "8p"}

		for _, section := range sections {
			result = getManResults(section, cmd)
			if len(result) > 0 {
				break
			}
		}
	}

	if len(result) < 1 {
		result = "Sorry, no manual page found."
	}

	return
}

func cmdMonkeyStab(r Recipient, chName, args string) (result string) {
	if len(args) < 1 || strings.EqualFold(args, CONFIG["mentionName"]) {
		args = r.MentionName
	}

	result = fmt.Sprintf("_unleashes a troop of pen-wielding stabbing-monkeys on %s!_\n", args)
	return
}

func cmdOid(r Recipient, chName, args string) (result string) {
	oids := strings.Split(args, " ")
	if len(args) < 1 || len(oids) != 1 {
		result = "Usage: " + COMMANDS["oid"].Usage
		return
	}

	oid := strings.TrimSpace(oids[0])

	theUrl := fmt.Sprintf("%s%s", COMMANDS["oid"].How, oid)
	urlArgs := map[string]string{"ua": "true"}
	data := getURLContents(theUrl, urlArgs)

	info_key := ""
	found := false
	info := map[string]string{}

	asn_re := regexp.MustCompile(`(?i)^\s*<textarea.*readonly>({.*})</textarea>`)
	info_re := regexp.MustCompile(`(?i)^\s*<br><strong>(.*)</strong>:`)

	for _, line := range strings.Split(string(data), "\n") {
		if m := asn_re.FindStringSubmatch(line); len(m) > 0 {
			info["ASN.1 notation"] = m[1]
			continue
		}

		if m := info_re.FindStringSubmatch(line); len(m) > 0 {
			found = true
			info_key = m[1]
			continue
		}

		if strings.Contains(line, "<br><br>") {
			found = false
			if info_key == "Information" {
				break
			} else {
				continue
			}
		}

		if found {
			oneLine := dehtmlify(line)
			if len(oneLine) > 1 {
				if _, ok := info[info_key]; !ok {
					info[info_key] = oneLine
				} else {
					info[info_key] += "\n" + oneLine
				}
			}
		}
	}

	if len(info) < 1 {
		result = fmt.Sprintf("No info found for OID '%s'.", oid)
	} else {
		var keys []string
		for k, _ := range info {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			result += fmt.Sprintf("%s: %s\n", k, info[k])
		}
	}

	return
}

func cmdOnion(r Recipient, chName, args string) (result string) {
	search := false
	theUrl := COMMANDS["onion"].How + "rss"

	if len(args) > 0 {
		theUrl = fmt.Sprintf("%ssearch?q=%s", COMMANDS["onion"].How, url.QueryEscape(args))
		search = true
	}

	data := getURLContents(theUrl, nil)

	if !search {
		items := strings.Split(string(data), "<item>")
		rss_re := regexp.MustCompile(`^<title>(.*)</title><link>(.*)</link>`)
		for _, item := range items {
			m := rss_re.FindStringSubmatch(item)
			if len(m) > 0 {
				result += m[1] + " - " + m[2] + "\n"
				return
			}
		}
	}

	found := false
	next := false
	search_re := regexp.MustCompile(`href="(.*)" rel="nofollow"><div>([^<]+)<`)
	for _, line := range strings.Split(string(data), "js_link") {
		if strings.Contains(line, ">Search<") {
			found = true
			continue
		}
		if strings.Contains(line, ">News in Brief<") {
			next = true
			continue
		}
		if found && next {
			m := search_re.FindStringSubmatch(line)
			if len(m) > 0 {
				result = m[2] + " - " + m[1]
				return
			}
		}
	}

	result = fmt.Sprintf("No results found on '%s'.", theUrl)
	return
}

func cmdOncall(r Recipient, chName, args string) (result string) {
	oncall := args
	oncall_source := "user input"
	if len(strings.Fields(oncall)) < 1 {
		if ch, found := getChannel(r.ChatType, r.ReplyTo); found {
			if r.ChatType == "hipchat" {
				oncall = r.ReplyTo
			} else {
				oncall = ch.Name
			}
			oncall_source = "channel name"
			if v, found := ch.Settings["oncall"]; found {
				oncall = v
				oncall_source = "channel setting"
			}
		} else {
			result = "Usage: " + COMMANDS["oncall"].Usage
			return
		}
	}

	oncallFound := true
	result += cmdOncallOpsGenie(r, chName, oncall, true)
	if len(result) < 1 {
		result = fmt.Sprintf("No oncall information found for '%s'.\n", oncall)
		oncallFound = false
	}

	if strings.HasPrefix(result, "No OpsGenie schedule found for") {
		oncallFound = false
	}

	if !oncallFound {
		switch oncall_source {
		case "channel name":
			result += fmt.Sprintf("\nIf your oncall rotation does not match your channel name (%s), use '!set oncall=<rotation_name>'.\n", chName)
		case "channel setting":
			result += fmt.Sprintf("\nIs your 'oncall' channel setting (%s) correct?\n", oncall)
			result += "If not, use '!set oncall=<rotation_name>' to fix that.\n"
		}
	}
	return
}

func cmdPing(r Recipient, chName, args string) (result string) {
	ping := "ping"
	hosts := strings.Fields(args)
	if len(hosts) > 1 {
		result = "Usage: " + COMMANDS["ping"].Usage
		return
	}

	if len(hosts) == 0 {
		result = "pong"
		return
	} else if strings.ToLower(hosts[0]) == strings.ToLower(CONFIG["mentionName"]) {
		result = "I'm alive!"
		return
	}

	host := fqdn(hosts[0])
	if len(host) < 1 {
		if strings.Contains(hosts[0], ".") {
			result = fmt.Sprintf("Unable to resolve %s.", hosts[0])
		} else {
			replies := []string{
				fmt.Sprintf("YO, @%s, WAKE UP!", hosts[0]),
				fmt.Sprintf("@%s, somebody needs you!", hosts[0]),
				fmt.Sprintf("ECHO REQUEST -> @%s", hosts[0]),
				fmt.Sprintf("You there, @%s?", hosts[0]),
				fmt.Sprintf("Hey, @%s, @%s is looking for you.", hosts[0], r.MentionName),
				fmt.Sprintf("_nudges %s._", hosts[0]),
				fmt.Sprintf("_pings %s._", hosts[0]),
				fmt.Sprintf("_pokes %s._", hosts[0]),
				fmt.Sprintf("_taps %s on the head._", hosts[0]),
			}
			result = replies[rand.Intn(len(replies))]
		}
		return
	}

	/* Alright, alright, we're being lazy here,
	 * but treating anything with a ':' as an IPv6
	 * address is actually good enough. */
	if strings.Contains(host, ":") {
		ping = "ping6"
		/* Yahoo only. :-/ */
		result = "Sorry, I'm running on an IPv4 only system."
		result += "\nI know, I know, that's quite silly, but it is what it is."
		return
	}

	_, err := runCommand(fmt.Sprintf("%s -q -w 1 -W 0.5 -i 0.5 -c 1 %s", ping, host))
	if err > 0 {
		result = fmt.Sprintf("Unable to ping %s.", hosts[0])
	} else {
		result = fmt.Sprintf("%s is alive.", hosts[0])
	}

	return
}

func cmdPraise(r Recipient, chName, args string) (result string) {
	if _, found := CHANNELS[chName]; !found {
		result = "This command only works in a channel."
		return
	}

	if len(args) < 1 {
		result = "Usage: " + COMMANDS["praise"].Usage
		return
	}

	expandedUser := expandSlackUser(args)
	if expandedUser != nil && expandedUser.ID != "" {
		args = expandedUser.Name
	}
	if strings.EqualFold(args, "me") ||
		strings.EqualFold(args, "myself") ||
		strings.EqualFold(args, r.MentionName) {
		result = cmdInsult(r, chName, "me")
		return
	}

	incrementCounter("praised", args)
	if strings.EqualFold(args, CONFIG["mentionName"]) {
		rand.Seed(time.Now().UnixNano())
		result = THANKYOU[rand.Intn(len(THANKYOU))]
	} else {
		result = fmt.Sprintf("%s: %s\n", args,
			randomLineFromUrl(COMMANDS["praise"].How, false))
	}
	return
}

func cmdPwgen(r Recipient, chName, args string) (result string) {
	arguments := strings.Fields(args)
	if len(arguments) > 3 {
		result = "Usage: " + COMMANDS["pwgen"].Usage
		return
	}

	theUrl := COMMANDS["pwgen"].How + "?nohtml=1"
	var i int
	lines := 1

	for n, a := range arguments {
		if _, err := fmt.Sscanf(a, "%d", &i); err != nil {
			result = "'" + a + "' does not look like a number to me."
			return
		}
		if i < 0 || i > 50 {
			result = "Please try a number between 0 and 50."
			return
		}

		if n == 0 {
			theUrl += "&num=" + a
		} else if n == 1 {
			theUrl += "&count=" + a
			lines = i
		} else {
			theUrl += "&complex=1"
		}
	}

	data := string(getURLContents(theUrl, nil))
	for n, line := range strings.Split(string(data), "\n") {
		if n < lines {
			result += line + "\n"
		}
	}
	return
}

func cmdQuote(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["quote"].Usage
		return
	}

	result = fmt.Sprintf("\"%s\"", args)

	args = strings.ToUpper(args)
	theURL := fmt.Sprintf("%s%s", COMMANDS["quote"].How, url.QueryEscape(args))
	data := getURLContents(theURL, nil)

	type Quote struct {
		FullExchangeName           string
		FiftyTwoWeekRange          struct{ Fmt string }
		RegularMarketPreviousClose struct{ Fmt string }
		RegularMarketOpen          struct{ Fmt string }
		RegularMarketDayRange      struct{ Fmt string }
		ShortName                  string
	}

	type YahooFinance struct {
		Context struct {
			Dispatcher struct {
				Stores struct {
					StreamDataStore struct {
						QuoteData map[string]Quote
					}
				}
			}
		}
	}

	var jsonString string
	re := regexp.MustCompile(`(?i).*root.App.main = (.*});`)
	for _, l := range strings.Split(string(data), "\n") {
		if m := re.FindStringSubmatch(l); len(m) > 0 {
			jsonString = m[1]
			break
		}
	}

	if len(jsonString) < 1 {
		result = fmt.Sprintf("Unable to get json data from '%s'.", theURL)
		return
	}

	var y YahooFinance
	err := json.Unmarshal([]byte(jsonString), &y)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal json data: %s\n", err)
		return
	}

	for q, d := range y.Context.Dispatcher.Stores.StreamDataStore.QuoteData {
		if q == args {
			result = fmt.Sprintf("<%s|%s (%s)> trading on '%s':\n```", theURL, q, d.ShortName, d.FullExchangeName)
			result += fmt.Sprintf("Previous Close: $%s\n", d.RegularMarketPreviousClose.Fmt)
			result += fmt.Sprintf("Open          : $%s\n", d.RegularMarketOpen.Fmt)
			result += fmt.Sprintf("Day Range     : $%s\n", d.RegularMarketDayRange.Fmt)
			result += fmt.Sprintf("52 Week Range : $%s\n```", d.FiftyTwoWeekRange.Fmt)
		}
	}
	return
}

func cmdRfc(r Recipient, chName, args string) (result string) {
	rfcs := strings.Split(args, " ")
	if len(rfcs) != 1 {
		result = "Usage: " + COMMANDS["rfc"].Usage
		return
	}

	rfc := strings.ToLower(strings.TrimSpace(rfcs[0]))

	if !strings.HasPrefix(rfc, "rfc") {
		rfc = "rfc" + rfc
	}

	theUrl := fmt.Sprintf("%s%s", COMMANDS["rfc"].How, rfc)
	data := getURLContents(theUrl, nil)

	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "<span class=\"h1\">") {
			result = dehtmlify(line)
			break
		}
	}

	if len(result) > 0 {
		result += "\n" + theUrl
	} else {
		result = "No such RFC."
	}

	return
}

func cmdRoom(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["room"].Usage
		return
	}

	room := strings.TrimSpace(args)
	lroom := strings.ToLower(room)

	type roomTopic struct {
		Name  string
		Topic string
	}

	var candidates []*roomTopic

	if r.ChatType == "hipchat" {
		for _, aRoom := range HIPCHAT_ROOMS {
			lc := strings.ToLower(aRoom.Name)

			if lc == lroom || aRoom.RoomId == room {
				result = fmt.Sprintf("'%s' (%s)\n", aRoom.Name, aRoom.Privacy)
				result += fmt.Sprintf("Topic: %s\n", aRoom.Topic)

				owner := strings.Split(aRoom.Owner, "@")[0]
				if u, found := HIPCHAT_ROSTER[owner]; found {
					result += fmt.Sprintf("Owner: %s\n", u.MentionName)
				}

				if aRoom.LastActive != "" {
					result += fmt.Sprintf("Last Active: %s\n", aRoom.LastActive)
				}

				if aRoom.NumParticipants != "0" {
					result += fmt.Sprintf("Hip Chatters: %s\n", aRoom.NumParticipants)
				}
				result += fmt.Sprintf("https://%s.hipchat.com/history/room/%s\n", CONFIG["hcService"], aRoom.RoomId)
				return
			} else {
				if strings.Contains(lc, lroom) {
					candidates = append(candidates, &roomTopic{aRoom.Name, aRoom.Topic})
				}
			}
		}
	} else if r.ChatType == "slack" {
		for _, ch := range SLACK_CHANNELS {
			lc := strings.ToLower(ch.Name)
			if lc == lroom {
				result = fmt.Sprintf("'%s'\n", ch.Name)
				if len(ch.Topic.Value) > 0 {
					result += fmt.Sprintf("Topic: %s\n", ch.Topic.Value)
				}
				if len(ch.Purpose.Value) > 0 {
					result += fmt.Sprintf("Purpose: %s\n", ch.Purpose.Value)
				}
				creator, err := SLACK_CLIENT.GetUserInfo(ch.Creator)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Unable to find user information for '%s'.\n", ch.Creator)
					result += fmt.Sprintf("Creator: Unknown\n")
				} else {
					result += fmt.Sprintf("Creator: %s\n", creator.Name)
				}
				result += fmt.Sprintf("# of members: %d\n", len(getAllMembersInChannel(ch.ID)))
				result += fmt.Sprintf("https://%s/messages/%s/\n", CONFIG["slackService"], lroom)
				return
			} else if strings.Contains(lc, lroom) {
				candidates = append(candidates, &roomTopic{ch.Name, ch.Topic.Value})
			}
		}
	}

	if len(candidates) > 0 {
		result = "No room with that exact name found.\n"
		if len(candidates) > 1 {
			result += "Some possible candidates might be:\n"
		} else {
			result += "Did you mean:\n"
		}
		for i, aRoom := range candidates {
			if i > 6 {
				result += "..."
				break
			}
			result += fmt.Sprintf("%s - %s\n", aRoom.Name, aRoom.Topic)
		}
	}

	if len(result) < 1 {
		if r.ChatType == "hipchat" {
			HIPCHAT_CLIENT.RequestRooms()
		}
		result = "No such room: " + room
	}

	return
}

func cmdSeen(r Recipient, chName, args string) (result string) {
	wanted := strings.Split(args, " ")
	user := wanted[0]
	verbose(4, "Looking in %s", r.ReplyTo)

	ch, found := getChannel(r.ChatType, r.ReplyTo)

	if len(wanted) > 1 {
		chName = wanted[1]
		slack_channel_re := regexp.MustCompile(`(?i)<(#[A-Z0-9]+)\|([^>]+)>`)
		m := slack_channel_re.FindStringSubmatch(wanted[1])
		if len(m) > 0 {
			chName = m[2]
		}
		verbose(4, "Looking for %s in %s'...", user, chName)
		ch, found = getChannel(r.ChatType, chName)
	}

	if strings.EqualFold(args, CONFIG["mentionName"]) {
		rand.Seed(time.Now().UnixNano())
		replies := []string{
			"You can't see me, I'm not really here.",
			"_is invisible._",
			"_looked, but only saw its shadow._",
			"Wed Dec 31 19:00:00 EST 1969",
		}
		result = replies[rand.Intn(len(replies))]
		return
	}

	if !found {
		if len(wanted) > 1 {
			result = "I'm not currently in #" + wanted[1]
		} else {
			result = "Ask me about a user in a channel."
		}
		return
	}

	if len(user) < 1 {
		result = fmt.Sprintf("Usage: %s", COMMANDS["seen"].Usage)
		return
	}

	if r.ChatType == "hipchat" {
		for u, info := range ch.HipChatUsers {
			if u.MentionName == user {
				result = info.Seen
			}
		}
	} else if r.ChatType == "slack" {
		if info, found := ch.SlackUsers[user]; found {
			result = info.Seen
		}
	}

	if len(result) < 1 {
		result = fmt.Sprintf("I have not seen that user in #%s.", ch.Name)
	}
	return
}

func cmdSet(r Recipient, chName, args string) (result string) {
	input := strings.SplitN(args, "=", 2)
	if len(args) > 1 && len(input) != 2 {
		result = "Usage:\n" + COMMANDS["set"].Usage
		return
	}

	var ch *Channel
	var found bool
	if ch, found = CHANNELS[chName]; !found {
		result = "I can only set things in a channel."
		return
	}

	if len(args) < 1 {
		if len(ch.Settings) < 1 {
			result = fmt.Sprintf("There currently are no settings for #%s.", chName)
			return
		}
		for n, v := range ch.Settings {
			result += fmt.Sprintf("%s=%s\n", n, v)
		}
		return
	}

	name := strings.TrimSpace(input[0])
	value := strings.TrimSpace(input[1])

	/* Users sometimes call "!set oncall=<team>" with
	* the literal brackets; let's help them. */
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimPrefix(value, "&lt;")
	value = strings.TrimSuffix(value, ">")
	value = strings.TrimSuffix(value, "&gt;")

	if len(ch.Settings) < 1 {
		ch.Settings = map[string]string{}
	}

	old := ""
	if old, found = ch.Settings[name]; found {
		old = fmt.Sprintf(" (was: %s)", old)
	}

	ch.Settings[name] = value

	result = fmt.Sprintf("Set '%s' to '%s'%s.", name, value, old)
	return
}

func cmdSms(r Recipient, chName, args string) (result string) {
	lookupType := "number"
	shortcode := args
	if len(args) < 1 {
		shortcode = "773786" // Yahoo! Shortcode
	}

	shortcode = strings.Replace(shortcode, "-", "", -1)

	var i int
	if _, err := fmt.Sscanf(shortcode, "%d", &i); err != nil {
		lookupType = "search"
	}

	var theUrl string
	if lookupType == "number" {
		theUrl = fmt.Sprintf("%sshort-code-%s/", COMMANDS["sms"].How, shortcode)
	} else if lookupType == "search" {
		theUrl = fmt.Sprintf("%s?fwp_short_code_search=%s/", COMMANDS["sms"].How, url.QueryEscape(shortcode))
	}
	data := getURLContents(theUrl, nil)

	printNext := false
	info := []string{
		"Business/Organization:",
		"Short Code Activation Date:",
		"Short Code Deactivation Date:",
		"Campaign Name:",
	}
	for _, line := range strings.Split(string(data), "\n") {
		if lookupType == "number" {
			if printNext {
				result += dehtmlify(line) + "\n"
				printNext = false
			}
			for _, field := range info {
				if strings.Contains(line, fmt.Sprintf("<td>%s</td>", field)) {
					result += field + " "
					printNext = true
					break
				}
			}
		} else if lookupType == "search" {
			re := regexp.MustCompile(`(?i)<h3><a href="(https://usshortcodedirectory.com/directory/short-code-([0-9]+)/)">(.*)</a></h3>`)
			if m := re.FindStringSubmatch(line); len(m) > 0 {
				result += m[3] + ": " + m[2]
				result += "\n" + m[1] + "\n"
			}
		}
	}

	if len(result) > 0 && lookupType == "number" {
		result = "Short Code: " + shortcode + "\n" + result
	}

	if len(result) < 1 {
		result = "No results found for '" + args + "'."
	}
	return
}

func cmdSpeb(r Recipient, chName, args string) (result string) {
	if len(args) > 0 {
		result = "Usage: " + COMMANDS["speb"].Usage
		return
	}

	result = randomLineFromUrl(COMMANDS["speb"].How, false)
	return
}

func cmdStfu(r Recipient, chName, args string) (result string) {
	var ch *Channel
	var found bool

	if ch, found = CHANNELS[chName]; !found {
		result = "This command only works in a channel."
		return
	}

	chatter := make(map[int][]string)

	if r.ChatType == "hipchat" {
		for u := range ch.HipChatUsers {
			if (len(args) > 0) && (u.MentionName != args) {
				continue
			}
			chatter[ch.HipChatUsers[u].Count] = append(chatter[ch.HipChatUsers[u].Count], u.MentionName)
		}
	} else if r.ChatType == "slack" {
		for u := range ch.SlackUsers {
			if (len(args) > 0) && (u != args) {
				continue
			}
			chatter[ch.SlackUsers[u].Count] = append(chatter[ch.SlackUsers[u].Count], u)
		}
	}

	if (len(args) > 0) && (len(chatter) < 1) {
		result = fmt.Sprintf("%s hasn't said anything in %s.",
			args, chName)
		return
	}

	var stfu []int
	for count := range chatter {
		stfu = append(stfu, count)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(stfu)))

	var chatterers []string
	for _, n := range stfu {
		for _, t := range chatter[n] {
			chatterers = append(chatterers, fmt.Sprintf("%s (%d)", t, n))
		}
	}

	i := len(chatterers)
	if i > 10 {
		i = 10
	}
	result += strings.Join(chatterers[0:i], ", ")
	return
}

func cmdTfln(r Recipient, chName, args string) (result string) {
	if len(args) > 1 {
		result = "Usage: " + COMMANDS["tfln"].Usage
		return
	}

	data := getURLContents(COMMANDS["tfln"].How, nil)

	tfln_re := regexp.MustCompile(`(?i)^<p><a href="/Text-Replies`)
	for _, line := range strings.Split(string(data), "\n") {
		if tfln_re.MatchString(line) {
			result = dehtmlify(line)
			return
		}
	}
	return
}

func cmdThrottle(r Recipient, chName, args string) (result string) {
	input := strings.Split(args, " ")
	if len(input) > 2 {
		result = "Usage: " + COMMANDS["throttle"].Usage
		return
	}

	newThrottle := DEFAULT_THROTTLE
	if len(input) == 2 {
		if _, err := fmt.Sscanf(input[1], "%d", &newThrottle); err != nil {
			result = "Invalid number of seconds."
			return
		}

		if newThrottle < 0 {
			result = cmdInsult(r, chName, "me")
			return
		}
	}

	var ch *Channel
	var found bool
	if ch, found = CHANNELS[chName]; !found {
		result = "I can only throttle things in a channel."
		return
	}

	if len(args) > 1 {
		d, err := time.ParseDuration(fmt.Sprintf("%ds", newThrottle-DEFAULT_THROTTLE))
		if err != nil {
			result = fmt.Sprintf("Unable to parse new duration: %s", err)
			return
		}
		ch.Throttles[input[0]] = time.Now().Add(d)
		result = fmt.Sprintf("%s => %d", input[0], newThrottle)
		return
	}

	var throttles []string
	if len(ch.Throttles) == 0 {
		result = "This channel is currently unthrottled."
		return
	}

	result = "These are the throttles for this channel:\n"
	for t, v := range ch.Throttles {
		duration := math.Ceil(DEFAULT_THROTTLE - time.Since(v).Seconds())
		if duration < 0 {
			duration = 0
		}
		throttles = append(throttles, fmt.Sprintf("%s => %v", t, duration))
	}
	sort.Strings(throttles)
	result += strings.Join(throttles, ", ")
	return
}

func cmdTime(r Recipient, chName, args string) (result string) {
	timezones := []string{"Asia/Taipei", "Asia/Calcutta", "UTC", "EST5EDT", "PST8PDT"}
	if len(args) > 0 {
		timezones = []string{args}
	}

	for _, l := range timezones {
		if loc, err := time.LoadLocation(l); err == nil {
			result += fmt.Sprintf("%s\n", time.Now().In(loc).Format(time.UnixDate))
		} else if loc, err := time.LoadLocation(strings.ToUpper(l)); err == nil {
			result += fmt.Sprintf("%s\n", time.Now().In(loc).Format(time.UnixDate))
		} else {
			var tz string
			var found bool

			address := getUserAddress(l)
			if len(address) > 0 {
				tz, found = locationToTZ(address)
			} else {
				tz, found = getColoTZ(l)
			}
			if !found {
				tz, _ = locationToTZ(l)
			}

			if loc, err := time.LoadLocation(tz); err == nil {
				result += fmt.Sprintf("%s\n", time.Now().In(loc).Format(time.UnixDate))
			} else {
				result = fmt.Sprintf("Can't determine a valid timezone for '%s'.", l)
			}
			return
		}
	}

	return
}

func cmdTld(r Recipient, chName, args string) (result string) {
	input := strings.Fields(args)
	if len(args) < 1 || len(input) != 1 {
		result = "Usage: " + COMMANDS["tld"].Usage
		return
	}

	domain := input[0]

	if strings.HasPrefix(domain, ".") {
		domain = domain[1:]
	}

	command := strings.Fields(COMMANDS["tld"].How)
	command = append(command, domain)

	data, _ := runCommand(command...)

	info := map[string]string{}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "domain:") {
			found = true
			continue
		}

		if found && strings.Contains(line, ":") {
			fields := strings.SplitN(line, ": ", 2)
			if _, found := info[fields[0]]; !found {
				info[fields[0]] = strings.TrimSpace(fields[1])
			}
		}
	}

	if len(info) < 1 {
		result = fmt.Sprintf("No such TLD: '%s'", domain)
	} else {
		if len(info["organisation"]) > 0 {
			result = fmt.Sprintf("```Organization: %s\n", info["organisation"])
		}
		if len(info["e-mail"]) > 0 {
			result += fmt.Sprintf("Contact     : %s\n", info["e-mail"])
		}
		if len(info["whois"]) > 0 {
			result += fmt.Sprintf("Whois       : %s\n", info["whois"])
		}
		result += fmt.Sprintf("Status      : %s\n", info["status"])
		result += fmt.Sprintf("Created     : %s```\n", info["created"])
		if len(info["remarks"]) > 0 {
			result += fmt.Sprintf("%s\n", strings.Replace(info["remarks"], "Registration information: ", "", -1))
		}
	}
	return
}

func cmdToggle(r Recipient, chName, args string) (result string) {
	wanted := "all"
	if len(args) > 1 {
		words := strings.Split(args, " ")
		if len(words) > 1 {
			result = "Usage: " + COMMANDS["toggle"].Usage
			return
		}
		wanted = words[0]
	}

	if ch, found := CHANNELS[chName]; found {
		if wanted == "all" {
			var toggles []string
			result = "These are the toggles for this channel:\n"
			for t, v := range ch.Toggles {
				toggles = append(toggles, fmt.Sprintf("%s => %v", t, v))
			}
			sort.Strings(toggles)
			result += strings.Join(toggles, ", ")
			return
		}
		if t, found := ch.Toggles[wanted]; found {
			ch.Toggles[wanted] = !t
			result = fmt.Sprintf("%s set to %v", wanted, ch.Toggles[wanted])
		} else {
			if _, found := TOGGLES[wanted]; found {
				if len(ch.Toggles) == 0 {
					ch.Toggles = map[string]bool{}
				}
				ch.Toggles[wanted] = true
				result = fmt.Sprintf("%s set to true", wanted)
			} else {
				result = fmt.Sprintf("No such toggle: %s", wanted)
			}
		}
	}
	return
}

func cmdResetCounter(r Recipient, chName, input string) (result string) {

	if CONFIG["botOwner"] != r.MentionName {
		result = fmt.Sprintf("Sorry, %s is not allowed to run this command.", r.MentionName)
		return
	}

	_, err := getCounter(input)
	if len(err) > 0 {
		result = err
		return
	} else {
		COUNTERS[input] = map[string]int{}
		result = input + " reset."
	}
	return
}

func cmdTop(r Recipient, chName, input string) (result string) {
	counter, err := getCounter(input)
	if len(err) > 0 {
		result = err
		return
	}

	nums := getSortedKeys(counter, true)
	n := 1
	counts := []string{}
	sep := ", "
	if input == "replies" {
		sep = "\n"
	}

	for _, k := range nums {
		counts = append(counts, fmt.Sprintf("%s (%d)", k, counter[k]))
		n++
		if n > 10 {
			break
		}
	}
	result += strings.Join(counts, sep)

	return
}

func cmdTrivia(r Recipient, chName, args string) (result string) {
	if len(args) > 0 {
		result = "Usage: " + COMMANDS["trivia"].Usage
		return
	}

	result = randomLineFromUrl(COMMANDS["trivia"].How, false)
	return
}

func cmdTroutSlap(r Recipient, chName, args string) (result string) {
	if len(args) < 1 || strings.EqualFold(args, CONFIG["mentionName"]) {
		args = r.MentionName
	}

	result = fmt.Sprintf("_pulls out a foul-smelling trout and slaps %s across the face._\n", args)
	return
}

func cmdUd(r Recipient, chName, args string) (result string) {

	theUrl := COMMANDS["ud"].How
	if len(args) > 0 {
		theUrl += fmt.Sprintf("define.php?term=%s", url.QueryEscape(args))
	} else {
		rand.Seed(time.Now().UnixNano())
		n := rand.Intn(1000)
		theUrl += fmt.Sprintf("random.php?page=%d", n)
	}

	data := getURLContents(theUrl, nil)
	desc_re := regexp.MustCompile(`(?i)/><meta content="(.*?)" name="twitter:description" `)
	example_re := regexp.MustCompile(`(?i)<div class="example">(.*?)</div>`)
	tags_re := regexp.MustCompile(`(?i)<div class="tags">(.*?)</div>`)
	notfound_re := regexp.MustCompile(`(?i)<div class="term space">(Sorry, we couldn't find: .*?)</div>`)
	word_re := regexp.MustCompile(`(?i)url=http%3A%2F%2F(.*?).urbanup.com`)

	description := ""
	example := ""
	tags := ""
	word := ""
	for _, line := range strings.Split(string(data), "\n") {
		if m := desc_re.FindStringSubmatch(line); len(m) > 0 {
			description = dehtmlify(m[1])
		}
		if m := example_re.FindStringSubmatch(line); len(m) > 0 {
			example = "Example: " + dehtmlify(m[1])
		}
		if m := tags_re.FindStringSubmatch(line); len(m) > 0 {
			tags = "Tags:" + strings.Join(strings.Split(dehtmlify(m[1]), "#"), " #")
		}
		if m := word_re.FindStringSubmatch(line); len(m) > 0 {
			word = m[1] + ":\n"
		}
		if strings.Contains(line, "<a class=\"circle-link\"") {
			break
		}

		if m := notfound_re.FindStringSubmatch(line); len(m) > 0 {
			result += "¯\\_(ツ)_/¯\n" + m[1]
			return
		}
	}

	if len(args) > 0 {
		word = ""
	}
	result = fmt.Sprintf("%s%s\n%s\n%s\n", word, description, example, tags)
	return
}

func cmdUnset(r Recipient, chName, args string) (result string) {
	input := strings.Fields(args)
	if len(input) != 1 {
		result = "Usage: " + COMMANDS["unset"].Usage
		return
	}

	var ch *Channel
	var found bool
	if ch, found = CHANNELS[chName]; !found {
		result = "I can only set things in a channel."
		return
	}

	if len(ch.Settings) < 1 {
		ch.Settings = map[string]string{}
	}

	old := ""
	if old, found = ch.Settings[args]; found {
		delete(ch.Settings, args)
		result = fmt.Sprintf("Deleted %s=%s.", args, old)
	} else {
		result = fmt.Sprintf("No such setting: '%s'.", args)
	}

	return
}

func cmdUnthrottle(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["unthrottle"].Usage
		return
	}

	var ch *Channel
	var found bool
	if ch, found = CHANNELS[chName]; !found {
		result = "I can only throttle things in a channel."
		return
	}

	if args == "*" || args == "everything" {
		for t, _ := range ch.Throttles {
			delete(ch.Throttles, t)
		}
	} else {
		delete(ch.Throttles, args)
	}

	replies := []string{
		"Okiley, dokiley!",
		"Sure thing, my friend!",
		"Done.",
		"No problemo.",
		"_throttles that thang._",
		"Got it.",
		"Word.",
		"Unthrottled to the max!",
		"Consider it done.",
	}
	result = replies[rand.Intn(len(replies))]
	return
}

func cmdUser(r Recipient, chName, args string) (result string) {
	if r.ChatType == "slack" {
		result = "Sorry, this feature only works for HipChat right now."
		return
	}

	if len(args) < 1 {
		result = "Usage: " + COMMANDS["user"].Usage
		return
	}

	user := strings.TrimSpace(args)
	candidates := []*hipchat.User{}

	for _, u := range HIPCHAT_ROSTER {
		uid := strings.SplitN(strings.Split(u.Id, "@")[0], "_", 2)[1]
		email := strings.Split(u.Email, "@")[0]
		if strings.EqualFold(u.Name, user) ||
			strings.EqualFold(email, user) ||
			strings.EqualFold(u.MentionName, user) ||
			strings.EqualFold(uid, user) {
			result = fmt.Sprintf("%s <%s> (%s)", u.Name, u.Email, u.MentionName)
			return
		} else {
			lc := strings.ToLower(u.Name)
			luser := strings.ToLower(user)
			lemail := strings.ToLower(u.Email)
			lmention := strings.ToLower(u.MentionName)
			if strings.Contains(lc, luser) ||
				strings.Contains(lemail, luser) ||
				strings.Contains(lmention, luser) {
				candidates = append(candidates, u)
			}
		}

	}

	if len(candidates) > 0 {
		result = "No user with that exact name found.\n"
		if len(candidates) > 1 {
			result += "Some possible candidates might be:\n"
		} else {
			result += "Did you mean:\n"
		}
		for i, u := range candidates {
			if i > 6 {
				result += "..."
				break
			}
			result += fmt.Sprintf("%s <%s> (%s)\n", u.Name, u.Email, u.MentionName)
		}
	}

	if len(result) < 1 {
		HIPCHAT_CLIENT.RequestUsers()
		result = "No such user: " + user
	}

	return
}

func cmdVu(r Recipient, chName, args string) (result string) {
	nums := strings.Split(args, " ")
	if len(nums) != 1 {
		result = "Usage: " + COMMANDS["vu"].Usage
		return
	}

	num := strings.TrimSpace(nums[0])

	if strings.HasPrefix(num, "#") {
		num = num[1:]
	}

	theUrl := fmt.Sprintf("%s%s", COMMANDS["vu"].How, num)
	data := getURLContents(theUrl, nil)

	info := []string{}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Vulnerability Note VU#") {
			found = true
			continue
		}

		if found {
			if strings.Contains(line, "<h2>") {
				info = append(info, dehtmlify(line))
				continue
			}
			if strings.Contains(line, "<p>") {
				info = append(info, dehtmlify(line))
				break
			}
		}
	}

	if len(info) < 1 {
		result = fmt.Sprintf("No info found for '%s'.", num)
	} else {
		result = strings.Join(info, "\n")
		result += fmt.Sprintf("\n%s", theUrl)
	}

	return
}

func cmdWeather(r Recipient, chName, args string) (result string) {
	apikey := CONFIG["openweathermapApiKey"]
	if len(apikey) < 1 {
		result = "Missing OpenWeatherMap API Key."
		return
	}

	if len(args) < 1 {
		args = r.MentionName
	}

	u := expandSlackUser(args)
	if u != nil && u.ID != "" {
		args = u.Name
	}

	if args == CONFIG["mentionName"] {
		args = "ne1"
	}

	address := getUserAddress(args)
	if len(address) > 0 {
		addressFields := strings.Split(address, ",")
		n := len(addressFields)
		if n > 1 {
			args = fmt.Sprintf("%s, %s", strings.TrimSpace(addressFields[n-2]),
				strings.TrimSpace(addressFields[n-1]))
		} else {
			args = address
		}
	} else {
		var unused Recipient
		coloInfo := cmdColo(unused, "", args)
		r := regexp.MustCompile(`(?m)Location\s+: (.+)`)
		if m := r.FindStringSubmatch(coloInfo); len(m) > 0 {
			args = m[1]
		}
	}

	latlon := cmdLatLong(r, chName, args)

	query := "weather?appid=" + apikey + "&"
	if strings.Contains(latlon, ",") {
		ll := strings.SplitN(latlon, ",", 2)
		query += fmt.Sprintf("lat=%s&lon=%s", ll[0], ll[1])
	} else {
		re := regexp.MustCompile(`^([0-9-]+)(,.*)?$`)
		if re.MatchString(args) {
			query += "zip="
		} else {
			query += "q="
		}
		query += url.QueryEscape(args)
	}

	theURL := fmt.Sprintf("https://api.openweathermap.org/data/2.5/%s", query)
	data := getURLContents(theURL, nil)

	type OpenWeatherMapResult struct {
		Coord struct {
			Lat float64
			Lon float64
		}
		Main struct {
			Humidity float64
			Pressure float64
			Temp     float64
			Temp_max float64
			Temp_min float64
		}
		Id   int
		Name string
		Sys  struct {
			Country string
			Sunrise int64
			Sunset  int64
		}
		Weather []struct {
			Description string
			Main        string
		}
		Wind struct {
			Deg   float64
			Speed float64
		}
	}

	var w OpenWeatherMapResult
	err := json.Unmarshal(data, &w)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal weather data: %s\n", err)
		return
	}

	if len(w.Name) < 1 {
		result = fmt.Sprintf("Sorry, location '%s' not found.\n", args)
		return
	}

	result = fmt.Sprintf("Weather in %s, %s: <https://openweathermap.org/city/%d|%s>\n", w.Name, w.Sys.Country,
		w.Id,
		w.Weather[0].Description)
	result += fmt.Sprintf("Temperature: %s (low: %s, high: %s)\n",
		tempStringFromKelvin(w.Main.Temp),
		tempStringFromKelvin(w.Main.Temp_min),
		tempStringFromKelvin(w.Main.Temp_max))
	result += fmt.Sprintf("Wind: %.1f m/s\n", w.Wind.Speed)
	result += fmt.Sprintf("Humidity: %.2f\n", w.Main.Humidity)
	result += fmt.Sprintf("Pressure: %.2f hpa\n", w.Main.Pressure)

	gmapLink := fmt.Sprintf("https://www.google.com/maps/@%f,%f,12z", w.Coord.Lat, w.Coord.Lon)
	result += fmt.Sprintf("Coordinates: <%s|[%.3f, %.3f]>\n", gmapLink, w.Coord.Lat, w.Coord.Lon)
	return
}

func tempStringFromKelvin(t float64) (s string) {
	c := t - 273.15
	f := c*9/5 + 32

	s = fmt.Sprintf("%.2f F / %.2f C", f, c)
	return
}

func cmdWhocyberedme(r Recipient, chName, args string) (result string) {
	if len(args) > 1 {
		result = "Usage: " + COMMANDS["whocyberedme"].Usage
		return
	}

	data := getURLContents(COMMANDS["whocyberedme"].How, nil)

	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "confirms") {
			result = dehtmlify(l)
			break
		}
	}
	return
}

func cmdWhois(r Recipient, chName, args string) (result string) {
	if len(strings.Fields(args)) != 1 {
		result = "Usage: " + COMMANDS["whois"].Usage
		return
	}

	hostinfo := cmdHost(r, chName, args)
	if strings.Contains(hostinfo, "not found:") {
		result = hostinfo
		return
	}

	out, _ := runCommand(fmt.Sprintf("whois %s", args))
	data := string(out)

	/* whois formatting is a mess; different whois servers return
	 * all sorts of different information in all sorts of different
	 * formats. We'll try to catch some common ones here. :-/ */

	var format string
	found := false

	formatGuesses := map[*regexp.Regexp]string{
		regexp.MustCompile("(?i)Registry Domain ID:"):                "common",
		regexp.MustCompile("(?i)%% This is the AFNIC Whois server."): "afnic",
		regexp.MustCompile("(?i)% Copyright.* by DENIC"):             "denic",
		regexp.MustCompile("(?i)The EDUCAUSE Whois database"):        "educause",
		regexp.MustCompile("(?i)for .uk domain names"):               "uk",
	}

	for p, f := range formatGuesses {
		if p.MatchString(data) {
			format = f
			found = true
		}
	}

	info := map[string]string{}
	var wanted []string
	var field string
	next := false

	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "No match for domain") {
			result = l
			return
		}

		if strings.HasPrefix(l, "%") || strings.HasPrefix(l, "#") {
			continue
		}

		if found {
			keyval := strings.SplitN(l, ":", 2)
			k := strings.TrimSpace(keyval[0])
			if len(keyval) > 1 {
				v := strings.TrimSpace(keyval[1])
				if _, exists := info[k]; exists {
					info[k] += ", " + v
				} else {
					info[k] = v
				}
			}

			if format == "common" {
				wanted = []string{
					"Registrar",
					"Registrar URL",
					"Updated Date",
					"Creation Date",
					"Registry Expiry Date",
					"Registrant Name",
					"Registrant Organization",
					"Registrant Country",
					"Registrant Email",
					"Name Server",
					"DNSSEC",
				}
			} else if format == "afnic" {
				if strings.HasPrefix(l, "nic-hdl:") {
					break
				}
				wanted = []string{
					"registrar",
					"country",
					"Expiry Date",
					"created",
					"last-update",
					"nserver",
					"e-mail",
				}
			} else if format == "denic" {
				wanted = []string{
					"Nserver",
					"Changed",
					"Organisation",
					"CountryCode",
					"Email",
				}
				if strings.HasPrefix(l, "[Zone-C]") {
					break
				}
			} else if format == "educause" {
				wanted = []string{
					"Registrant",
					"Email",
					"Name Servers",
					"Domain record activated",
					"Domain record last updated",
					"Domain expires",
				}
				if strings.HasPrefix(l, "Registrant:") {
					field = "Registrant"
					next = true
					continue
				}

				if strings.Contains(l, "@") {
					info["Email"] = strings.TrimSpace(l)
					continue
				}

				if strings.HasPrefix(l, "Name Servers") {
					field = "Name Servers"
					next = true
					continue
				}

				if next {
					if field == "Name Servers" {
						if s, exists := info[field]; exists {
							if len(s) > 1 {
								info[field] += "\n" + strings.TrimSpace(l)
							} else {
								info[field] = strings.TrimSpace(l)
							}
						} else {
							info[field] = strings.TrimSpace(l)
						}
					} else {
						info[field] = strings.TrimSpace(l)
						next = false
					}
					if len(l) < 1 {
						next = false
					}
				}
			} else if format == "uk" {
				wanted = []string{
					"Registrant",
					"Regsitrar",
					"Registered on",
					"Expiry date",
					"Last updated",
					"Name servers",
				}
				if strings.Contains(l, "Registrant:") {
					field = "Registrant"
					next = true
					continue
				}
				if strings.Contains(l, "Registrar:") {
					field = "Registrar"
					next = true
					continue
				}
				if strings.Contains(l, "Name servers:") {
					field = "Name servers"
					next = true
					continue
				}

				if next {
					if strings.Contains(l, "WHOIS lookup made") {
						break
					}
					if field == "Name servers" {
						if s, exists := info[field]; exists {
							if len(s) > 1 {
								info[field] += "\n" + strings.TrimSpace(l)
							} else {
								info[field] = strings.TrimSpace(l)
							}
						} else {
							info[field] = strings.TrimSpace(l)
						}
					} else {
						info[field] = strings.TrimSpace(l)
						next = false
					}
				}
			}
		}
	}

	if len(info) > 0 {
		for _, f := range wanted {
			if v, exists := info[f]; exists {
				result += fmt.Sprintf("%s: %s\n", f, v)
			}
		}
	}
	return
}

func cmdWiki(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["wiki"].Usage
		return
	}

	query := url.QueryEscape(args)
	theUrl := fmt.Sprintf("%s%s", COMMANDS["wiki"].How, query)
	data := getURLContents(theUrl, nil)

	/* json results are:
	 * [ "query",
	 *   ["terms", ...],
	 *   ["first sentence", ...],
	 *   [["url", ...]
	 * ]
	 */
	var jsonData []interface{}
	err := json.Unmarshal(data, &jsonData)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal wiki data: %s\n", err)
		return
	}

	if len(jsonData) < 4 {
		result = fmt.Sprintf("Something went bump when getting wiki json for '%s'.", args)
		return
	}

	sentences := jsonData[2]
	urls := jsonData[3]

	if len(sentences.([]interface{})) < 1 {
		result = fmt.Sprintf("No Wikipedia page found for '%s'.", args)
		return
	}

	index := 0
	result = sentences.([]interface{})[0].(string)

	if strings.HasSuffix(result, " may refer to:") ||
		strings.HasSuffix(result, " commonly refers to:") {
		index = 1
		result = sentences.([]interface{})[index].(string)
	}

	if len(urls.([]interface{})) > 0 {
		result += "\n" + urls.([]interface{})[index].(string)
	}
	return
}

func cmdWtf(r Recipient, chName, args string) (result string) {
	if len(args) < 1 {
		result = "Usage: " + COMMANDS["wtf"].Usage
		return
	}
	terms := strings.Split(args, " ")
	if (len(terms) > 2) || ((len(terms) == 2) && (terms[0] != "is")) {
		result = "Usage: " + COMMANDS["wtf"].Usage
		return
	}

	term := terms[0]
	if len(terms) == 2 {
		term = terms[1]
	}

	// Slack expands '#channel' to e.g. '<#CBEAWGAPJ|channel>'
	slack_channel_re := regexp.MustCompile(`(?i)<(#[A-Z0-9]+)\|([^>]+)>`)
	m := slack_channel_re.FindStringSubmatch(term)
	if len(m) > 0 {
		result = getChannelInfo(m[1])
		if len(result) > 0 {
			return
		} else {
			term = m[2]
		}
	}

	slack_user := term
	u := expandSlackUser(term)
	if u != nil && u.ID != "" {
		slack_user = u.Name
	}
	if slack_user != term {
		result = cmdBy(r, "", slack_user)
		if len(result) > 0 {
			if strings.HasPrefix(result, "No such user") {
				term = slack_user
			} else {
				return
			}
		}
	}

	if term == CONFIG["mentionName"] {
		result = fmt.Sprintf("Unfortunately, no one can be told what %s is...\n", CONFIG["mentionName"])
		result += "You have to see it for yourself."
		return
	}

	if term == "pi" {
		result = fmt.Sprintf("%.64v", math.Pi)
		return
	}

	out, _ := runCommand(fmt.Sprintf("ywtf %s", term))
	result = string(out)

	if strings.HasPrefix(result, "ywtf: ") {
		result = result[6:]
	}

	return
}

func cmdXkcd(r Recipient, chName, args string) (result string) {
	latest := false
	theUrl := COMMANDS["xkcd"].How
	if len(args) < 1 {
		theUrl = "https://xkcd.com/"
		latest = true
	} else if _, err := strconv.Atoi(args); err == nil {
		result = "https://xkcd.com/" + args
		return
	} else {
		theUrl += "process?action=xkcd&query=" + url.QueryEscape(args)
	}

	data := getURLContents(theUrl, nil)
	xkcd_re := regexp.MustCompile(`^Permanent link to this comic: (https://xkcd.com/[0-9]+/)`)
	for n, line := range strings.Split(string(data), "\n") {
		m := xkcd_re.FindStringSubmatch(line)
		if latest {
			if len(m) > 0 {
				result = dehtmlify(m[1])
				break
			}
		} else if n == 2 {
			xkcd := strings.Split(line, " ")[0]
			result = "https://xkcd.com/" + xkcd + "/"
		}
	}

	return
}

func cmdYubifail(r Recipient, chName, args string) (result string) {
	result = getCountable("yubifail", chName, r, args)
	return
}

/*
 * General Functions
 */

func argcheck(flag string, args []string, i int) {
	if len(args) <= (i + 1) {
		fail(fmt.Sprintf("'%v' needs an argument\n", flag))
	}
}

func catchPanic() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "Panic!\n%s\n", r)
		debug.PrintStack()
		fmt.Fprintf(os.Stderr, "Let's try this again.\n")
		if len(CONFIG["hcService"]) > 0 {
			doTheHipChat()
		}
		if len(CONFIG["slackName"]) > 0 {
			doTheSlackChat()
		}
	}
}

func createCommands() {
	COMMANDS["8ball"] = &Command{cmdEightBall,
		"ask the magic 8-ball",
		"builtin",
		"!8ball <question>",
		nil}
	COMMANDS["asn"] = &Command{cmdAsn,
		"display information about ASN",
		"whois -h whois.cymru.com",
		"!asn [<host>|<ip>|<asn>)",
		nil}
	COMMANDS["bacon"] = &Command{cmdBacon,
		"everybody needs more bacon",
		"mostly pork",
		"!bacon",
		nil}
	COMMANDS["beer"] = &Command{cmdBeer,
		"quench your thirst",
		"https://www.beeradvocate.com/",
		"!beer <beer>",
		nil}
	COMMANDS["bs"] = &Command{cmdBs,
		"Corporate B.S. Generator",
		"builtin, but inspired from http://www.atrixnet.com/bs-generator.html",
		"!bs",
		nil}
	COMMANDS["cert"] = &Command{cmdCert,
		"display information about the x509 cert found at the given hostname",
		"crypto/tls",
		"!cert fqdn [<sni>] [chain]",
		[]string{"certs"}}
	COMMANDS["channels"] = &Command{cmdChannels,
		"display channels I'm in",
		"builtin",
		"!channels",
		nil}
	COMMANDS["cidr"] = &Command{cmdCidr,
		"display CIDR information",
		"builtin (net.ParseCIDR)",
		"!cidr <cidr>",
		nil}
	COMMANDS["clear"] = &Command{cmdClear,
		"clear the screen / backlog",
		"builtin",
		"!clear [num]",
		nil}
	COMMANDS["cowsay"] = &Command{cmdCowsay,
		"moo!",
		"cowsay(1)",
		"!cowsay <msg>",
		nil}
	COMMANDS["curses"] = &Command{cmdCurses,
		"check your curse count",
		"builtin",
		"!curses [<user>]",
		nil}
	COMMANDS["fml"] = &Command{cmdFml,
		"display a quote from www.fmylife.com",
		"http://www.fmylife.com/random",
		"!fml",
		nil}
	COMMANDS["fortune"] = &Command{cmdFortune,
		"print a random, hopefully interesting, adage",
		"fortune(1)",
		"!fortune",
		[]string{"motd"}}
	COMMANDS["giphy"] = &Command{cmdGiphy,
		"get a gif from giphy",
		"https://api.giphy.com/v1/gifs/search",
		"!giphy",
		[]string{"gif"}}
	COMMANDS["help"] = &Command{cmdHelp,
		"display this help",
		"builtin",
		"!help [all|<command>]",
		[]string{"?", "commands", "hlp"}}
	COMMANDS["host"] = &Command{cmdHost,
		"host lookup",
		"host(1)",
		"!host <host>",
		nil}
	COMMANDS["how"] = &Command{cmdHow,
		"show how a command is implemented",
		"builtin",
		"!how <command>",
		nil}
	COMMANDS["img"] = &Command{cmdImage,
		"post a link to an image",
		"https://images.search.yahoo.com/search/images?p=",
		"!img <search term>",
		[]string{"image", "pic"}}
	COMMANDS["info"] = &Command{cmdInfo,
		"display info about a channel",
		"builtin",
		"!info <channel>",
		nil}
	COMMANDS["insult"] = &Command{cmdInsult,
		"insult somebody",
		"http://www.pangloss.com/seidel/Shaker/index.html",
		"!insult <somebody>",
		nil}
	COMMANDS["jira"] = &Command{cmdJira,
		"display info about a jira ticket",
		URLS["jira"] + "/rest/api/latest/issue/",
		"!jira <ticket>",
		nil}
	COMMANDS["latlong"] = &Command{cmdLatLong,
		"look up latitude and longitude for a given location",
		"https://www.latlong.net/",
		"!latlong location",
		[]string{"coords"}}
	COMMANDS["leave"] = &Command{nil,
		"cause me to leave the current channel",
		"builtin",
		"!leave",
		nil}
	COMMANDS["log"] = &Command{cmdLog,
		"show the URL of a room's logs",
		"HipChat API",
		"!log [room]",
		nil}
	COMMANDS["man"] = &Command{cmdMan,
		"summarize manual page",
		"http://man7.org/linux/man-pages/",
		"!man [<section>] <command>",
		nil}
	COMMANDS["monkeystab"] = &Command{cmdMonkeyStab,
		"unleash a troop of pen-wielding stabbing monkeys",
		"builtin",
		"!monkeystab <something>",
		nil}
	COMMANDS["oid"] = &Command{cmdOid,
		"display OID information",
		"http://oid-info.com/cgi-bin/display?action=display&oid=",
		"!oid <oid>",
		nil}
	COMMANDS["oncall"] = &Command{cmdOncall,
		"show who's oncall",
		"Service Now & OpsGenie",
		"!oncall [<group>]\nIf <group> is not specified, this uses the channel name.\nUse '!set oncall=<rotation name>' to change the default.",
		[]string{"on_call"}}
	COMMANDS["onion"] = &Command{cmdOnion,
		"get your finest news headlines",
		"https://www.theonion.com/",
		"!onion [<term>]",
		nil}
	COMMANDS["ping"] = &Command{cmdPing,
		"try to ping hostname",
		"ping(1)",
		"!ping <hostname>",
		nil}
	COMMANDS["praise"] = &Command{cmdPraise,
		"praise somebody",
		URLS["praise"],
		"!praise <somebody>",
		[]string{"compliment"}}
	COMMANDS["pwgen"] = &Command{cmdPwgen,
		"generate a password for you",
		URLS["pwgen"],
		"!pwgen [length] [count] [complex]",
		nil}
	COMMANDS["quote"] = &Command{cmdQuote,
		"show stock price information",
		"https://finance.yahoo.com/quote/",
		"!quote <symbol>",
		[]string{"stock"}}
	COMMANDS["reset"] = &Command{cmdResetCounter,
		"reset a global counter (requires bot admin privs)",
		"builtin",
		"!reset <counter>",
		nil}
	COMMANDS["rfc"] = &Command{cmdRfc,
		"display title and URL of given RFC",
		"https://tools.ietf.org/html/",
		"!rfc <rfc>",
		nil}
	COMMANDS["room"] = &Command{cmdRoom,
		"show information about the given chat room",
		"HipChat / Slack API",
		"!room <name>",
		[]string{"channel"}}
	COMMANDS["seen"] = &Command{cmdSeen,
		"show last time <user> was seen in <channel>",
		"builtin",
		"!seen <user> [<channel>]",
		nil}
	COMMANDS["set"] = &Command{cmdSet,
		"set a channel setting",
		"builtin",
		"!set -- show all current settings\n" +
			"!set name=value -- set 'name' to 'value'\n",
		[]string{"setting"}}
	COMMANDS["sms"] = &Command{cmdSms,
		"show short code information",
		"https://usshortcodedirectory.com/directory/",
		"!sms <numbers>",
		nil}
	COMMANDS["speb"] = &Command{cmdSpeb,
		"show a security problem excuse bingo result",
		/* http://crypto.com/bingo/pr */
		URLS["speb"],
		"!speb",
		[]string{"secbingo"}}
	COMMANDS["stfu"] = &Command{cmdStfu,
		"show channel chatterers",
		"builtin",
		"!stfu [<user>]",
		nil}
	COMMANDS["tfln"] = &Command{cmdTfln,
		"display a text from last night",
		"http://www.textsfromlastnight.com/Random-Texts-From-Last-Night.html",
		"!tfln",
		nil}
	COMMANDS["throttle"] = &Command{cmdThrottle,
		"show current throttles",
		"builtin",
		"!throttle -- show all throttles in this channel\n" +
			fmt.Sprintf("!throttle <something>  -- set throttle for <something> to %d seconds\n", DEFAULT_THROTTLE) +
			"!throttle <something> <seconds> -- set throttle for <something> to <seconds>\n" +
			"Note: I will happily let you set throttles I don't know or care about.",
		nil}
	COMMANDS["time"] = &Command{cmdTime,
		"show the current time",
		"builtin",
		"!time [TZ]",
		nil}
	COMMANDS["tld"] = &Command{cmdTld,
		"show what TLD is",
		"whois -h whois.iana.org",
		"!tld <tld>",
		nil}
	COMMANDS["toggle"] = &Command{cmdToggle,
		"toggle a feature",
		"builtin",
		"!toggle [<feature>]",
		nil}
	COMMANDS["top"] = &Command{cmdTop,
		"display top 10 stats of <counter>",
		"builtin",
		"!top <counter>",
		nil}
	COMMANDS["trivia"] = &Command{cmdTrivia,
		"show a random piece of trivia",
		URLS["trivia"],
		"!trivia",
		nil}
	COMMANDS["troutslap"] = &Command{cmdTroutSlap,
		"troutslap a sucker",
		"builtin",
		"!troutslap <something>",
		nil}
	COMMANDS["ud"] = &Command{cmdUd,
		"look up a term using the Urban Dictionary (NSFW)",
		"https://www.urbandictionary.com/",
		"!ud [<term>]",
		nil}
	COMMANDS["unset"] = &Command{cmdUnset,
		"unset a channel setting",
		"builtin",
		"!unset name",
		nil}
	COMMANDS["unthrottle"] = &Command{cmdUnthrottle,
		"unset a throttle",
		"builtin",
		"!unthrottle <throttle> -- remove given throttle for this channel\n" +
			"Note: I will happily pretend to unthrottle throttles I don't know or care about.",
		nil}
	COMMANDS["user"] = &Command{cmdUser,
		"show information about the given HipChat user",
		"HipChat API",
		"!user <name>",
		nil}
	COMMANDS["vu"] = &Command{cmdVu,
		"display summary of a CERT vulnerability",
		"https://www.kb.cert.org/vuls/id/",
		"!vu <num>",
		nil}
	COMMANDS["weather"] = &Command{cmdWeather,
		"show weather information",
		"https://api.openweathermap.org/data/2.5/",
		"!weather <location>",
		nil}
	COMMANDS["whois"] = &Command{cmdWhois,
		"show whois information",
		"whois(1)",
		"!whois <domain>",
		nil}
	COMMANDS["whocyberedme"] = &Command{cmdWhocyberedme,
		"show who cybered you",
		"https://whocybered.me",
		"!whocyberedme",
		[]string{"attribution"}}
	COMMANDS["wiki"] = &Command{cmdWiki,
		"look up a term on Wikipedia",
		"https://en.wikipedia.org/w/api.php?action=opensearch&redirects=resolve&search=",
		"!wiki <something>",
		nil}
	COMMANDS["wtf"] = &Command{cmdWtf,
		"decrypt acronyms",
		"ywtf(1)",
		"!wtf <term>",
		[]string{"ywtf"}}
	COMMANDS["xkcd"] = &Command{cmdXkcd,
		"find an xkcd for you",
		"https://relevantxkcd.appspot.com/",
		"!xkcd <words>",
		nil}
	COMMANDS["yubifail"] = &Command{cmdYubifail,
		"check your yubifail count",
		"builtin",
		"!yubifail [<user>]",
		nil}
}

func jbotDebug(in interface{}) {
	if CONFIG["debug"] == "yes" {
		fmt.Fprintf(os.Stderr, "%v\n", in)
	}
}

func joinKnownChannels() {
	verbose(1, "Joining channels slack thinks I'm in...")

	var params slack.GetConversationsForUserParameters
	params.UserID = CONFIG["slackID"]
	params.Limit = 999
	params.Cursor = ""
	params.Types = []string{"public_channel", "private_channel"}

	channels, cursor, err := SLACK_CLIENT.GetConversationsForUser(&params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to GetConversationsForUser: %s\n", err)
		return
	}

	for cursor != "" {
		params.Cursor = cursor
		nextChannels, nextCursor, err := SLACK_CLIENT.GetConversationsForUser(&params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to GetConversationsForUser: %s\n", err)
			break
		}
		channels = append(channels, nextChannels...)
		cursor = nextCursor
	}

	for _, c := range channels {
		if _, found := CHANNELS[c.Name]; !found {
			ch := newSlackChannel(c.Name, c.ID, "Slack")
			CHANNELS[ch.Name] = &ch
		}
	}
}

func dehtmlify(in string) (out string) {
	out = in
	strip_html_re := regexp.MustCompile(`<.+?>`)
	out = strip_html_re.ReplaceAllString(out, "")

	strip_newline_re := regexp.MustCompile("\n")
	out = strip_newline_re.ReplaceAllString(out, "")

	out = html.UnescapeString(out)

	out = strings.TrimSpace(out)
	return
}

func doTheHipChat() {
	user := strings.Split(CONFIG["hcJabberID"], "@")[0]

	authType := "plain"
	pass := CONFIG["hcPassword"]
	if len(pass) < 1 {
		authType = "oauth"
		pass = CONFIG["hcOauthToken"]
	}

	var err error
	HIPCHAT_CLIENT, err = hipchat.NewClient(user, pass, "bot", authType)
	if err != nil {
		fail(fmt.Sprintf("Client error: %s\n", err))
	}

	HIPCHAT_CLIENT.Status("chat")
	HIPCHAT_CLIENT.RequestUsers()
	HIPCHAT_CLIENT.RequestRooms()

	for _, ch := range CHANNELS {
		verbose(1, "Joining HipChat channel #%s...", ch.Name)
		HIPCHAT_CLIENT.Join(ch.Id, CONFIG["fullName"])

		/* Our state file might not contain
		 * the changed structures, so explicitly
		 * fix things here. */
		if len(ch.HipChatUsers) < 1 {
			ch.HipChatUsers = make(map[hipchat.User]UserInfo, 0)
		}

		for t, v := range TOGGLES {
			if len(ch.Toggles) == 0 {
				ch.Toggles = map[string]bool{}
			}
			if _, found := ch.Toggles[t]; !found {
				ch.Toggles[t] = v
			}
		}
	}

	go hcPeriodics()
	go HIPCHAT_CLIENT.KeepAlive()

	go func() {
		defer catchPanic()

		for {
			select {
			case message := <-HIPCHAT_CLIENT.Messages():
				processHipChatMessage(message)
			case users := <-HIPCHAT_CLIENT.Users():
				updateRoster(users)
			case rooms := <-HIPCHAT_CLIENT.Rooms():
				updateHipChatRooms(rooms)
			}
		}
	}()
}

func doTheSlackChat() {
	SLACK_CLIENT = slack.New(CONFIG["slackToken"])
	if CONFIG["debug"] == "yes" {
		SLACK_CLIENT.SetDebug(true)
	}

	SLACK_RTM = SLACK_CLIENT.NewRTM()
	go SLACK_RTM.ManageConnection()

	/* If we introduced a new channel property,
	 * but the serialized data does not contain it, it
	 * would be undefined (e.g. 'off' / nonexistent
	 * for a toggle).  So here we
	 * quickly initialize all (unknown) data.
	 */
	updateChannels()

	joinKnownChannels()
	go updateSlackChannels()
	go slackPeriodics()
Loop:
	for {
		select {
		case msg := <-SLACK_RTM.IncomingEvents:
			switch ev := msg.Data.(type) {
			case *slack.ChannelJoinedEvent:
				processSlackChannelJoin(ev)

			case *slack.InvalidAuthEvent:
				fmt.Fprintf(os.Stderr, "Unable to authenticate.")
				break Loop

			case *slack.MessageEvent:
				processSlackMessage(ev)

			case *slack.RTMError:
				fmt.Fprintf(os.Stderr, "Slack error: %s\n", ev.Error())

			case *slack.UserChangeEvent:
				processSlackUserChangeEvent(ev)
			default:
				jbotDebug(msg)

			}
		}
	}
}

func expandSlackUser(in string) (u *slack.User) {
	// Slack expands '@user' to e.g. '<@CBEAWGAPJ>'
	slack_user_re := regexp.MustCompile(`(?i)<@([A-Z0-9]+)>`)
	m := slack_user_re.FindStringSubmatch(in)
	if len(m) > 0 {
		u, _ = SLACK_CLIENT.GetUserInfo(m[1])
	}

	return
}

func fail(msg string) {
	fmt.Fprintf(os.Stderr, msg)
	os.Exit(EXIT_FAILURE)
}

func findCommandAlias(cmd string) (alias string) {
	for name, command := range COMMANDS {
		for _, a := range command.Aliases {
			if a == cmd {
				return name
			}
		}
	}
	return
}

func fqdn(host string) (fqdn string) {
	/* Kinda like 'search' domains in /etc/resolv.conf. */
	tries := []string{
		host,
		fmt.Sprintf("%s.corp.yahoo.com", host),
		fmt.Sprintf("%s.yahoo.com", host),
	}

	for _, h := range tries {
		if _, err := net.LookupHost(h); err == nil {
			return h
		}
	}
	return
}

func getopts() {
	eatit := false
	args := os.Args[1:]
	for i, arg := range args {
		if eatit {
			eatit = false
			continue
		}
		switch arg {
		case "-D":
			CONFIG["debug"] = "yes"
			VERBOSITY = 10
		case "-V":
			printVersion()
			os.Exit(EXIT_SUCCESS)
		case "-c":
			eatit = true
			argcheck("-f", args, i)
			CONFIG["configFile"] = args[i+1]
		case "-h":
			usage(os.Stdout)
			os.Exit(EXIT_SUCCESS)
		case "-v":
			VERBOSITY++
		default:
			fmt.Fprintf(os.Stderr, "Unexpected option or argument: %v\n", args[i])
			usage(os.Stderr)
			os.Exit(EXIT_FAILURE)
		}
	}
}

func getAllMembersInChannel(id string) (allMembers []string) {
	params := slack.GetUsersInConversationParameters{
		ChannelID: id,
		Limit:     1000,
	}

	for {
		members, cursor, err := SLACK_CLIENT.GetUsersInConversation(&params)
		if err != nil {
			fmt.Printf("Unable to get conversation: %s\n", err)
			break
		}
		allMembers = append(allMembers, members...)
		if len(cursor) > 0 {
			params.Cursor = cursor
		} else {
			break
		}
	}

	return
}

func getChannel(chatType, id string) (ch *Channel, ok bool) {
	ok = false

	if chatType == "slack" {
		uId := strings.ToUpper(id)
		slackChannel, err := SLACK_CLIENT.GetConversationInfo(uId, false)
		if err == nil {
			id = slackChannel.Name
		}
	}

	ch, ok = CHANNELS[id]

	return
}

func getChannelInfo(id string) (info string) {
	var ch slack.Channel
	found := false
	if strings.HasPrefix(id, "#") {
		id = id[1:]
		ch, found = SLACK_CHANNELS[id]
	}

	if !found {
		c, err := SLACK_CLIENT.GetConversationInfo(id, false)
		if err != nil {
			return
		}
		ch = *c
	}

	topic := ""
	if len(ch.Topic.Value) > 0 {
		topic = fmt.Sprintf(" -- \"%s\"", ch.Topic.Value)
	}
	members := getAllMembersInChannel(id)
	info = fmt.Sprintf("%s (%d members)%s\n%s\n",
		ch.Name, len(members),
		topic, ch.Purpose.Value)
	return
}

func getCounter(c string) (counter map[string]int, err string) {
	cnt, ok := COUNTERS[c]
	if !ok {
		if len(c) > 0 {
			err = "I don't keep track of that.\n"
		}
		err += "These are the things I currently track:\n"
		var counters []string
		for c := range COUNTERS {
			counters = append(counters, c)
		}
		sort.Strings(counters)
		err += strings.Join(counters, ", ")
	} else {
		counter = cnt
	}
	return
}

func getManResults(section, cmd string) (result string) {
	nsection := section
	if strings.HasSuffix(section, "p") {
		nsection = string(section[0])
	}
	theUrl := fmt.Sprintf("%sman%s/%s.%s.html", COMMANDS["man"].How, nsection, cmd, section)
	data := getURLContents(theUrl, nil)

	section_re := regexp.MustCompile(`(?i)^<h2><a id="(NAME|SYNOPSIS|DESCRIPTION)" href="#`)
	p := false
	count := 0
	section = ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "</pre>") {
			p = false
			count = 0
		}
		if m := section_re.FindStringSubmatch(line); len(m) > 0 {
			if len(result) == 0 {
				result += "```"
			}
			section = m[1]
			result += "\n" + m[1]
			p = true
			count = 0
			continue
		}
		if p && count < 3 {
			count++
			result += "\n        " + dehtmlify(line)
		}

		if count == 3 {
			result += "\n        ..."
			p = false
			count = 0
			if section == "DESCRIPTION" {
				break
			}
		}
	}

	if len(result) > 0 {
		result += "```\n" + theUrl
	}

	return
}

func getRecipientFromMessage(mfrom string, chatType string) (r Recipient) {
	r.ChatType = chatType
	if chatType == "hipchat" {
		from := strings.Split(mfrom, "/")
		r.Id = from[0]
		r.ReplyTo = strings.SplitN(strings.Split(r.Id, "@")[0], "_", 2)[1]
		r.Name = ""
		r.MentionName = ""

		if len(from) > 1 {
			r.Name = from[1]
		}

		if len(r.Name) > 1 {
			for _, u := range HIPCHAT_ROSTER {
				if u.Name == r.Name {
					r.MentionName = u.MentionName
					break
				}
			}
		}
	} else if chatType == "slack" {
		/* Format is "user@channel"; if no
		 * "user" component, then we have a
		 * privmsg, which is a private
		 * channel. */

		index := 0
		if strings.HasPrefix(mfrom, "@") {
			index = 1
		}
		from := strings.Split(mfrom, "@")
		r.Id = strings.Trim(from[index], "@")
		r.ReplyTo = from[1]
		user, err := SLACK_CLIENT.GetUserInfo(r.Id)
		if err != nil {
			if bot, e := SLACK_CLIENT.GetBotInfo(r.Id); e == nil {
				r.Name = bot.Name
				r.MentionName = bot.Name
			}
			/* else: privmsg; let's just ignore it */
		} else {
			r.Name = user.Profile.RealName
			r.MentionName = user.Name
		}
	}

	return
}

/*
 * This function returns a sorted list of keys based
 * on hashmap values.  This allows you to then go
 * through the hash in sorted order.
 */
func getSortedKeys(hash map[string]int, rev bool) (sorted []string) {
	var vals []int
	for k := range hash {
		vals = append(vals, hash[k])
	}

	if rev {
		sort.Sort(sort.Reverse(sort.IntSlice(vals)))
	} else {
		sort.Ints(vals)
	}

	seen := map[int]bool{}
	for _, n := range vals {
		for k, v := range hash {
			if v == n && !seen[n] {
				sorted = append(sorted, k)
			}
		}
		seen[n] = true
	}
	return
}

/* Additional arguments can influence how the request is made:
 * - if args["by"] is "true", then the URL requires access credentials
 * - if args["corp"] is "true", then the URL requires a second type of credentials
 * - if args["ua"] is "true", then we fake the User-Agent
 * - if args["basic-auth-user"] is set, use that username for basic HTTP auth
 * - if args["basic-auth-password"] is set, use that password for basic HTTP auth
 * - if any args["header"] is set, use that value to set the given header
 *   set the given 'key=value' headers
 */
func getURLContents(givenUrl string, args map[string]string) (data []byte) {
	verbose(3, "Fetching %s...", givenUrl)
	jar, err := cookiejar.New(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize cookie jar: %s\n", err)
		return
	}

	u, err := url.Parse(givenUrl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to parse url '%s': %s\n", givenUrl, err)
		return
	}

	if by, ok := args["by"]; ok && by == "true" {
		_, err := bouncer.CheckLogin(givenUrl, COOKIES)
		if err != nil {
			if !strings.Contains(err.Error(), "URL mismatch") {
				fmt.Fprintf(os.Stderr, "+++ bouncer.CheckLogin failed: %s\n", err.Error())
				return
			}

			verbose(4, "BY Cookies expired - reloading.")
			loginOpt := bouncer.UseBouncer
			if corp, ok := args["corp"]; ok && corp == "true" {
				loginOpt = bouncer.UseGuesthouse
			}
			c, err := bouncer.Login(CONFIG["byUser"], CONFIG["byPassword"], loginOpt)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Unable to refresh BY cookie: %s\n", err)
				return
			}
			COOKIES = c
		}
		jar.SetCookies(u, COOKIES)
	}

	client := http.Client{
		Jar: jar,
	}

	request, err := http.NewRequest("GET", givenUrl, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create new request for '%s': %s\n", givenUrl, err)
		return
	}

	var ba_user string
	var ba_pass string

	for key, val := range args {
		if key == "ua" {
			request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/63.0.3239.132 Safari/537.36")
		} else if key == "basic-auth-user" {
			ba_user = val
		} else if key == "basic-auth-password" {
			ba_pass = val
		} else {
			request.Header.Set(key, val)
		}
	}

	if len(ba_user) > 0 {
		request.SetBasicAuth(ba_user, ba_pass)
	}

	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to GET '%s': %s\n", givenUrl, err)
		return
	}

	defer response.Body.Close()

	data, err = ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to read body of '%s': %s\n", givenUrl, err)
		return
	}

	return
}

/*
 * !countable -> your total 'countable' account
 * !countable @user -> that user's total countable count
 * !countable #channel -> total countable count in this channel
 *
 * These are handled in 'cmdTop':
 * !top countable -> top 5 countableers
 */
func getCountable(which, chName string, r Recipient, wanted string) (result string) {
	verbose(3, "Getting %s count for %s in %s, looking for %s...", which, r.MentionName, chName, wanted)

	channelLookup := false
	// Slack expands '#channel' to e.g. '<#CBEAWGAPJ|channel>'
	slack_channel_re := regexp.MustCompile(`(?i)<(#[A-Z0-9]+)\|([^>]+)>`)
	m := slack_channel_re.FindStringSubmatch(wanted)
	if len(m) > 0 {
		wanted = m[2]
		channelLookup = true
	}

	// Private channels may not be expanded by slack...
	if strings.HasPrefix(wanted, "#") {
		wanted = wanted[1:]
		channelLookup = true
	}

	if channelLookup {
		r.MentionName = "*"
		result = getUserCountableByChannel(which, wanted, r)
		return
	}

	chName = "*"
	if len(wanted) > 0 {
		expandedUser := expandSlackUser(wanted)
		if expandedUser != nil && expandedUser.ID != "" {
			r.MentionName = expandedUser.Name
		} else {
			r.MentionName = wanted
		}
	}

	result = getUserCountableByChannel(which, chName, r)
	return
}

func getUserCountableByChannel(countable, channel string, r Recipient) (result string) {
	verbose(3, "Getting %s count by channel for %s in %s...", countable, r.MentionName, channel)

	count := 0
	if channel == "*" {
		userCurses := map[string]int{}
		for _, ch := range CHANNELS {
			users := getUsersFromChannel(ch.Name, r.ChatType)
			if uinfo, found := users[r.MentionName]; found {
				if countable == "yubifail" {
					count += uinfo.Yubifail
				} else if countable == "curses" {
					for cw, count := range uinfo.CurseWords {
						userCurses[cw] += count
					}
				}
			}
		}

		if countable == "curses" {
			curseRanks := []string{}
			curses := getSortedKeys(userCurses, true)
			for _, c := range curses {
				curseRanks = append(curseRanks, fmt.Sprintf("%s (%d)", c, userCurses[c]))
			}
			if len(curseRanks) < 1 {
				result = fmt.Sprintf("Looks like %s has been behaving since I started paying attention...", r.MentionName)
			} else {
				result = strings.Join(curseRanks, ", ")
			}
			return
		}
	} else {
		_, found := CHANNELS[channel]
		if !found {
			result = fmt.Sprintf("I don't know anything about #%s.", channel)
			return
		}

		users := getUsersFromChannel(channel, r.ChatType)

		if r.MentionName == "*" {
			for _, info := range users {
				if countable == "yubifail" {
					count += info.Yubifail
				} else if countable == "curses" {
					curseRanks := []string{}
					curses := getSortedKeys(info.CurseWords, true)
					for _, c := range curses {
						curseRanks = append(curseRanks, fmt.Sprintf("%s (%d)", c, info.CurseWords[c]))
					}
					if len(curseRanks) < 1 {
						result = fmt.Sprintf("Looks like %s has been behaving (at least in #%s) since I started paying attention...", channel, r.MentionName)
					} else {
						result = strings.Join(curseRanks, ", ")
					}
					return
				}
			}
		} else {
			uinfo, found := users[r.MentionName]
			if !found {
				result = fmt.Sprintf("I don't think %s is in #%s.", r.MentionName, channel)
				return
			}
			if countable == "yubifail" {
				count += uinfo.Yubifail
			} else if countable == "curses" {
				count += uinfo.Curses
			}
		}
	}

	result = fmt.Sprintf("%d\n", count)
	return
}

func getUsersFromChannel(channel, chatType string) (users map[string]UserInfo) {
	ch, found := CHANNELS[channel]
	if !found {
		return
	}

	if chatType == "slack" {
		users = ch.SlackUsers
	} else {
		for hc, u := range ch.HipChatUsers {
			users[hc.MentionName] = u
		}
	}

	return
}

func incrementCounter(category, counter string) {
	if categoryCounters, ok := COUNTERS[category]; ok {
		if ccount, ok := categoryCounters[counter]; ok {
			categoryCounters[counter] = ccount + 1
		} else {
			categoryCounters[counter] = 1
		}
		COUNTERS[category] = categoryCounters
	} else {
		COUNTERS[category] = map[string]int{counter: 1}
	}
}

func isThrottled(throttle string, ch *Channel) (is_throttled bool) {
	is_throttled = false
	if ch.Throttles == nil {
		ch.Throttles = map[string]time.Time{}
	}

	if t, found := ch.Throttles[throttle]; found {
		duration := time.Since(t).Seconds()
		if duration < DEFAULT_THROTTLE {
			is_throttled = true
		} else {
			ch.Throttles[throttle] = time.Now()
		}
	} else {
		ch.Throttles[throttle] = time.Now()
	}
	return
}

func leave(r Recipient, channelFound bool, msg string, command bool) {
	verbose(2, "%s asked us to leave %s on %s.", r.Name, r.ReplyTo, r.ChatType)
	if !command && !strings.Contains(msg, "please") {
		reply(r, "Please ask politely.")
		return
	}

	if channelFound {
		if r.ChatType == "hipchat" {
			HIPCHAT_CLIENT.Part(r.Id, CONFIG["fullName"])
			delete(CHANNELS, r.ReplyTo)
		} else if r.ChatType == "slack" {
			msg := "Bots can't leave Slack channels - you'd have to find a Slack admin to kick me out.\n"
			msg += "But I'm going to ignore everything in this channel going forward.\n"
			msg += "If you do miss me terribly much, @-mention me and I'll start paying attention in here again, ok?\n\n"
			rand.Seed(time.Now().UnixNano())
			msg += cursiveText(GOODBYE[rand.Intn(len(GOODBYE))])
			ch, found := getChannel(r.ChatType, r.ReplyTo)
			if found {
				ch.Settings["ignored"] = "true"
				msg += fmt.Sprintf("\n_pretends to have left #%s._", ch.Name)
			}
			reply(r, msg)
		}
	} else {
		reply(r, "Try again from a channel I'm in.")
	}
	return
}

func locationToTZ(l string) (result string, success bool) {
	success = false

	apikey := CONFIG["timezonedbApiKey"]
	if len(apikey) < 1 {
		result = "Missing 'timezonedbApiKey'."
		return
	}

	lat := "0.0"
	lng := "0.0"

	latlon := cmdLatLong(Recipient{}, "", l)
	if !strings.Contains(latlon, ",") {
		result = "Unknown location."
		return
	}

	ll := strings.SplitN(latlon, ",", 2)
	lat = ll[0]
	lng = ll[1]

	theURL := fmt.Sprintf("http://api.timezonedb.com/v2.1/get-time-zone?key=%s&format=json&by=position&lat=%s&lng=%s",
		apikey, lat, lng)
	data := getURLContents(theURL, nil)

	type TZData struct {
		Abbreviation string
		CountryCode  string
		CountryName  string
		Dst          string
		Formatted    string
		GmtOFfset    int
		Status       string
		ZoneName     string
	}

	var t TZData

	err := json.Unmarshal(data, &t)
	if err != nil {
		result = fmt.Sprintf("Unable to unmarshal tz data: %s\n", err)
		return
	}

	result = t.ZoneName
	success = true

	return
}

func newSlackChannel(name, id, inviter string) (ch Channel) {
	verbose(2, "Creating new channel '#%s'...", name)

	ch.Toggles = map[string]bool{}
	ch.Throttles = map[string]time.Time{}
	ch.Settings = map[string]string{}
	ch.Type = "slack"
	ch.Id = id
	ch.SlackUsers = make(map[string]UserInfo, 0)
	ch.Inviter = "Nobody"
	ch.Name = name
	ch.Phishy = &PhishCount{0, 0, time.Now(), time.Unix(0, 0)}

	if len(inviter) > 0 {
		user, err := SLACK_CLIENT.GetUserInfo(inviter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to find user information for '%s'.\n", inviter)
		} else {
			ch.Inviter = user.Name
		}
	}

	for t, v := range TOGGLES {
		ch.Toggles[t] = v
	}

	return
}

func parseConfig() {
	fname := CONFIG["configFile"]
	verbose(1, "Parsing config file '%s'...", fname)
	fd, err := os.Open(fname)
	if err != nil {
		fail(fmt.Sprintf("Unable to open '%s': %v\n", fname, err))
	}
	defer fd.Close()

	n := 0
	input := bufio.NewReader(fd)
	for {
		data, err := input.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Unable to read input: %v\n", err)
			}
			break
		}

		/* Ignore everything after '#' */
		line := strings.Split(string(data), "#")[0]
		line = strings.TrimSpace(line)

		n++

		if len(line) == 0 {
			continue
		}

		keyval := strings.Split(line, "=")
		if len(keyval) != 2 {
			fail(fmt.Sprintf("Invalid line in configuration file '%s', line %d.",
				fname, n))
		} else {
			key := strings.TrimSpace(keyval[0])
			val := strings.TrimSpace(keyval[1])
			printval := val
			for _, s := range SECRETS {
				if key == s {
					printval = val[:4] + "..."
					break
				}
			}
			jbotDebug(fmt.Sprintf("Setting '%s' to '%s'...", key, printval))
			CONFIG[key] = val
		}
	}

	if len(CONFIG["hcService"]) > 0 {
		if len(CONFIG["hcPassword"]) > 0 && len(CONFIG["hcOauthToken"]) > 0 {
			fail("Please set *either* 'password' *or* 'oauth_token', not both.\n")
		} else if len(CONFIG["hcPassword"]) < 1 && len(CONFIG["hcOauthToken"]) < 1 {
			fail("You need to set either 'password' or 'oauth_token' in your config.\n")
		}

		if len(CONFIG["hcControlChannel"]) > 0 {
			var ch Channel

			verbose(2, "Setting up control channel '%s'...", CONFIG["hcControlChannel"])
			r := getRecipientFromMessage(CONFIG["hcControlChannel"], "hipchat")

			ch.Toggles = map[string]bool{}
			ch.Throttles = map[string]time.Time{}
			ch.Settings = map[string]string{}
			ch.Type = "hipchat"
			ch.Name = r.ReplyTo
			ch.Id = r.Id
			ch.HipChatUsers = make(map[hipchat.User]UserInfo, 0)
			for t, v := range TOGGLES {
				ch.Toggles[t] = v
			}
			jbotDebug(fmt.Sprintf("%v", ch))
			CHANNELS[ch.Name] = &ch
		}
	}

	if len(CONFIG["slackService"]) > 0 {
		if len(CONFIG["mentionName"]) < 1 || len(CONFIG["slackToken"]) < 0 {
			fail("Please set 'mentionName' and 'slackToken'.")
		}
	}
}

func hcPeriodics() {
	for _ = range time.Tick(PERIODICS * time.Second) {
		HIPCHAT_CLIENT.Status("chat")
		HIPCHAT_CLIENT.RequestUsers()
		HIPCHAT_CLIENT.RequestRooms()

		if len(CONFIG["hcControlChannel"]) > 0 {
			r := getRecipientFromMessage(CONFIG["hcControlChannel"], "hipchat")
			HIPCHAT_CLIENT.Say(r.Id, CONFIG["fullName"], "ping")
		}
	}
}

func printVersion() {
	fmt.Printf("%v version %v\n", PROGNAME, VERSION)
}

func processCommands(r Recipient, invocation, line string) {
	defer catchPanic()

	who := r.ReplyTo

	ch, channelFound := getChannel(r.ChatType, r.ReplyTo)
	if channelFound {
		who = ch.Name
	} else if r.ChatType == "slack" {
		if user, err := SLACK_CLIENT.GetUserInfo(r.Id); err == nil {
			who = user.Name
		}
	}

	args := strings.Fields(line)
	if len(args) < 1 {
		rand.Seed(time.Now().UnixNano())
		replies := []string{
			"Yes?",
			"Yeeeeees?",
			"How can I help you?",
			"You sound like you need help. Call a friend.",
			"What do you want?",
			"I can't help you unless you tell me what you want.",
			"Go on, don't be shy, ask me something.",
			"At your service!",
			"Ready to serve!",
			"Uhuh, sure.",
			"_looks at you expectantly._",
			"_chuckles._",
			"Go on...",
			"?",
			fmt.Sprintf("!%s", r.MentionName),
		}
		reply(r, replies[rand.Intn(len(replies))])
		return
	}

	verbose(2, "%s #%s: '%s'", r.ChatType, who, line)

	var cmd string
	if strings.EqualFold(args[0], CONFIG["mentionName"]) {
		args = args[1:]
	}

	if len(args) > 0 {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	}

	jbotDebug(fmt.Sprintf("|%s| |%s|", cmd, args))

	/* '!leave' does not have a callback, so needs
	 * to be processed first. */
	if cmd == "leave" {
		leave(r, channelFound, line, true)
		return
	}

	var response string
	_, commandFound := COMMANDS[cmd]

	if !commandFound {
		cm_re := regexp.MustCompile(`(?i)^cmr?([0-9]+)$`)
		inc_re := regexp.MustCompile(`(?i)^inc([0-9]+)$`)
		jira_re := regexp.MustCompile(`(?i)^([a-z]+-[0-9]+)$`)

		alias := findCommandAlias(cmd)
		if len(alias) > 1 {
			cmd = alias
			commandFound = true
		} else if m := cm_re.FindStringSubmatch(cmd); len(m) > 0 {
			cmd = "cm"
			args = []string{m[1]}
			commandFound = true
		} else if m := jira_re.FindStringSubmatch(cmd); len(m) > 0 {
			cmd = "jira"
			args = []string{m[1]}
			commandFound = true
		} else if m := inc_re.FindStringSubmatch(cmd); len(m) > 0 {
			cmd = "sn"
			args = []string{m[1]}
			commandFound = true
		} else if strings.HasPrefix(invocation, "!") {
			/* people get excited and say e.g. '!!' or '!!!'; ignore that */
			rex := regexp.MustCompile(`^[[:punct:]]+$`)
			if rex.MatchString(cmd) {
				return
			}
			response = cmdHelp(r, r.ReplyTo, cmd)
		} else if channelFound {
			processChatter(r, line, true)
			return
		}
	}

	if commandFound {
		incrementCounter("commands", cmd)
		if COMMANDS[cmd].Call != nil {
			chName := r.ReplyTo
			if ch, found := getChannel(r.ChatType, r.ReplyTo); found {
				chName = ch.Name
			}
			response = COMMANDS[cmd].Call(r, chName, strings.Join(args, " "))
		} else {
			fmt.Fprintf(os.Stderr, "'nil' function for %s?\n", cmd)
			return
		}
	}

	reply(r, response)
	return
}

func processHipChatInvite(r Recipient, invite string) {
	from := strings.Split(invite, "'")[1]
	fr := getRecipientFromMessage(from, "hipchat")
	inviter := strings.Split(fr.Id, "@")[0]
	channelName := r.ReplyTo

	var ch Channel
	ch.Toggles = map[string]bool{}
	ch.Throttles = map[string]time.Time{}
	ch.Settings = map[string]string{}
	ch.Name = r.ReplyTo
	ch.Type = "hipchat"
	ch.Id = r.Id
	if _, found := HIPCHAT_ROSTER[inviter]; found {
		ch.Inviter = HIPCHAT_ROSTER[inviter].MentionName
	} else {
		ch.Inviter = "Nobody"
	}
	ch.HipChatUsers = make(map[hipchat.User]UserInfo, 0)

	for t, v := range TOGGLES {
		ch.Toggles[t] = v
	}

	verbose(2, "I was invited into '%s' (%s) by '%s'.", channelName, r.Id, from)
	CHANNELS[channelName] = &ch
	verbose(1, "Joining HipChat #%s...", ch.Name)
	HIPCHAT_CLIENT.Join(r.Id, CONFIG["fullName"])
}

func processHipChatMessage(message *hipchat.Message) {
	if len(message.Body) < 1 {
		/* If a user initiates a 1:1 dialog
		 * with the bot, the hipchat client will send a ''
		 * ping even if they try to close the
		 * dialog.  If there is no data, we
		 * have no business replying or doing
		 * much of anything, so let's just
		 * return. */
		return
	}

	r := getRecipientFromMessage(message.From, "hipchat")
	if r.Name == CONFIG["fullName"] {
		//verbose("Ignoring message from myself.", 5)
		return
	}

	updateSeen(r, message.Body)

	if strings.HasPrefix(message.Body, "<invite from") {
		processHipChatInvite(r, message.Body)
		return
	}

	if len(r.Name) < 1 && len(r.MentionName) < 1 {
		verbose(3, "Ignoring channel topic message ('%s') in #%s.", message.Body, r.ReplyTo)
		return
	}

	processMessage(r, message.Body)
}

func processMessage(r Recipient, msg string) {
	p := fmt.Sprintf("^(?i)(!|[@/]%s [/!]?", CONFIG["mentionName"])

	if r.ChatType == "slack" {
		p += "|<@" + CONFIG["slackID"] + "> [/!]?"
	}
	p += ")"

	command_re := regexp.MustCompile(p)

	if command_re.MatchString(msg) {
		matchEnd := command_re.FindStringIndex(msg)[1]
		processCommands(r, msg[0:matchEnd], msg[matchEnd:])
	} else {
		processChatter(r, msg, false)
	}
}

func processSlackChannelJoin(ev *slack.ChannelJoinedEvent) {
	jbotDebug(fmt.Sprintf("Join: %v\n", ev))
}

func processSlackInvite(r Recipient, name string, msg *slack.MessageEvent) {
	if strings.Contains(msg.Text, "<@"+CONFIG["slackID"]+">") {
		ch := newSlackChannel(name, msg.Channel, msg.Inviter)
		verbose(2, "I was invited into Slack '%s' (%s) by '%s'.", ch.Name, ch.Id, ch.Inviter)
		CHANNELS[ch.Name] = &ch
		rand.Seed(time.Now().UnixNano())
		reply(r, HELLO[rand.Intn(len(HELLO))])
	}
}

func processSlackMessage(msg *slack.MessageEvent) {
	jbotDebug(fmt.Sprintf("\nMessage: |%v|", msg))

	LAST_SLACK_MESSAGE_TIME = time.Now()

	info := SLACK_RTM.GetInfo()

	var channelName string

	channel, err := SLACK_CLIENT.GetConversationInfo(msg.Channel, false)
	if err == nil {
		channelName = channel.Name
	}

	r := getRecipientFromMessage(fmt.Sprintf("%s@%s", msg.User, msg.Channel), "slack")

	ch, found := CHANNELS[channelName]
	if !found {
		/* Hey, let's just pretend that any
		 * message we get in a channel that
		 * we don't know about is effectively
		 * an invite. */
		processSlackInvite(r, channelName, msg)
		return
	} else {
		ignored := ch.Settings["ignored"]
		atMention := fmt.Sprintf("<@" + CONFIG["slackID"] + ">")
		if strings.EqualFold(ignored, "true") {
			if strings.Contains(msg.Text, atMention) {
				ch.Settings["ignored"] = "false"
			} else {
				return
			}
		}
	}

	if msg.User == info.User.ID {
		/* Ignore our own messages. */
		return
	}

	txt := msg.Text
	if msg.SubType == "message_changed" {
		txt = msg.SubMessage.Text
	}

	/* E.g. threads and replies get a dupe event with
	 * an empty text.  Let's ignore those right
	 * away. */
	if len(txt) < 1 {
		return
	}

	updateSeen(r, txt)

	/* Slack "helpfully" hyperlinks text that
	 * looks like a URL:
	 * "foo www.yahoo.com" becomes "foo <http://www.yahoo.com|www.yahoo.com>"
	 * Undo that nonsense.
	 *
	 * Note: Slack will also do all sorts of other
	 * encoding and linking, but to undo all of
	 * that would quickly become way too complex,
	 * so here we only undo the simplest cases to
	 * allow users to pass hostnames. */
	txt = SLACK_UNLINK_RE1.ReplaceAllString(txt, "${3}")
	txt = SLACK_UNLINK_RE2.ReplaceAllString(txt, "${1}")
	processMessage(r, txt)
}

func processSlackUserChangeEvent(ev *slack.UserChangeEvent) {
	if !ev.User.IsBot {
		return
	}

	newName := ev.User.Name
	oldReal := ev.User.Profile.RealName

	if oldReal == CONFIG["fullName"] {
		if newName != oldReal {
			verbose(1, "Bot was renamed from '%s' to '%s'!", oldReal, newName)
		}

		from := CONFIG["fullName"] + "@" + CONFIG["emailDomain"]
		to := []string{CONFIG["botOwner"] + "@" + CONFIG["emailDomain"]}
		subject := CONFIG["fullName"] + " bot change"
		body := fmt.Sprintf("New User Info:\n\n"+
			"ID: %s\n"+
			"TeamID: %s\n"+
			"Name: %s\n"+
			"Deleted: %v\n"+
			"RealName: %s\n"+
			"Profile:\n"+
			"  FirstName: %s\n"+
			"  LastName: %s\n"+
			"  RealName: %s\n"+
			"  Email: %s\n",
			ev.User.ID,
			ev.User.TeamID,
			ev.User.Name,
			ev.User.Deleted,
			ev.User.RealName,
			ev.User.Profile.FirstName,
			ev.User.Profile.LastName,
			ev.User.Profile.RealName,
			ev.User.Profile.Email)

		err := sendMailSMTP(from, to, []string{""}, subject, body)
		if len(err) > 0 {
			fmt.Fprintf(os.Stderr, "Unable to send bot change mail: %s\n", err)
			fmt.Fprintf(os.Stderr, "%v\n", ev)
		}
	}
}

func randomLineFromUrl(theUrl string, useBy bool) (line string) {
	rand.Seed(time.Now().UnixNano())
	urlArgs := map[string]string{"by": "false"}
	if useBy {
		urlArgs["by"] = "true"
	}
	data := getURLContents(theUrl, urlArgs)
	lines := strings.Split(string(data), "\n")
	line = lines[rand.Intn(len(lines))]
	return
}

func readSavedData() {
	verbose(2, "Reading saved data from: %s", CONFIG["channelsFile"])
	if _, err := os.Stat(CONFIG["channelsFile"]); err != nil {
		return
	}

	b, err := ioutil.ReadFile(CONFIG["channelsFile"])
	if err != nil {
		fail(fmt.Sprintf("Error %s: %q\n", CONFIG["channelsFile"], err))
	}

	buf := bytes.Buffer{}
	buf.Write(b)

	d := gob.NewDecoder(&buf)
	if err := d.Decode(&CHANNELS); err != nil {
		fail(fmt.Sprintf("Unable to decode data: %s\n", err))
	}

	verbose(2, "Reading saved data from: %s", CONFIG["countersFile"])
	if _, err := os.Stat(CONFIG["countersFile"]); err != nil {
		return
	}

	b, err = ioutil.ReadFile(CONFIG["countersFile"])
	if err != nil {
		fail(fmt.Sprintf("Error %s: %q\n", CONFIG["countersFile"], err))
	}

	buf = bytes.Buffer{}
	buf.Write(b)

	d = gob.NewDecoder(&buf)
	if err := d.Decode(&COUNTERS); err != nil {
		fail(fmt.Sprintf("Unable to decode data: %s\n", err))
	}
}

func reply(r Recipient, msg string) {
	incrementCounter("replies", msg)
	if r.ChatType == "hipchat" {
		if _, found := CHANNELS[r.ReplyTo]; found {
			HIPCHAT_CLIENT.Say(r.Id, CONFIG["fullName"], msg)
		} else {
			HIPCHAT_CLIENT.PrivSay(r.Id, CONFIG["fullName"], msg)
		}
	} else if r.ChatType == "slack" {
		recipient := r.ReplyTo
		channelName := "#"
		slackChannel, err := SLACK_CLIENT.GetConversationInfo(r.ReplyTo, false)
		if err == nil {
			channelName = slackChannel.Name
		} else {
			_, _, id, err := SLACK_RTM.OpenIMChannel(r.Id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Unable to open private channel: %s\n%v\n", err, r)
				return
			}
			recipient = id
		}

		for len(msg) > SLACK_MAX_LENGTH {
			verbose(3, "Message length %d > limit %d, chunking...\n", len(msg), SLACK_MAX_LENGTH)
			m1 := msg[:SLACK_MAX_LENGTH-1]

			last_index := strings.LastIndex(m1, "\n")
			if last_index == 0 {
				last_index = strings.LastIndex(m1, " ")
			}
			if last_index > 0 {
				m1 = msg[:last_index-1]
				msg = msg[last_index+1:]

				m1 = fontFormat(channelName, m1)
				SLACK_RTM.SendMessage(SLACK_RTM.NewOutgoingMessage(m1, recipient))
			} else {
				SLACK_RTM.SendMessage(SLACK_RTM.NewOutgoingMessage("Message too long, truncating...\n", recipient))
				SLACK_RTM.SendMessage(SLACK_RTM.NewOutgoingMessage(msg[:SLACK_MAX_LENGTH-1], recipient))
			}
		}
		msg = fontFormat(channelName, msg)
		SLACK_RTM.SendMessage(SLACK_RTM.NewOutgoingMessage(msg, recipient))
	}
}

func runCommand(cmd ...string) (out []byte, rval int) {
	var argv []string

	if len(cmd) == 0 {
		return
	}

	if len(cmd) == 1 {
		argv = strings.Split(dehtmlify(cmd[0]), " ")
	} else {
		for _, word := range cmd {
			argv = append(argv, dehtmlify(word))
		}
	}
	command := exec.Command(argv[0], argv[1:]...)

	rval = 0
	verbose(3, "Exec'ing '%s'...", argv)

	go func() {
		time.Sleep(30 * time.Second)
		if command != nil && command.ProcessState != nil &&
			command.ProcessState.Exited() != true {
			response := fmt.Sprintf("Sorry, I had to kill your '%s' command.\n", cmd)
			fmt.Fprintf(os.Stderr, ":: |%v|\n", command)
			if err := command.Process.Kill(); err != nil {
				response += fmt.Sprintf("Unable to kill your process: %s", err)
			}
			out = []byte(response)
		}
	}()

	tmp, err := command.CombinedOutput()
	if err != nil {
		rval = 1
		if len(out) < 1 && len(tmp) < 1 {
			out = []byte(fmt.Sprintf("%s", err))
		}
	}

	if len(out) < 1 {
		out = tmp
	}
	return
}

func serializeData() {
	verbose(1, "Serializing data...")

	gob.Register(Channel{})
	b := bytes.Buffer{}
	e := gob.NewEncoder(&b)
	if err := e.Encode(CHANNELS); err != nil {
		fmt.Fprintf(os.Stderr, "Unable to encode channels: %s\n", err)
		return
	}

	err := ioutil.WriteFile(CONFIG["channelsFile"], b.Bytes(), 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to write data to '%s': %s\n",
			CONFIG["channelsFile"], err)
		return
	}

	gob.Register(map[string]int{})
	b = bytes.Buffer{}
	e = gob.NewEncoder(&b)
	if err := e.Encode(COUNTERS); err != nil {
		fmt.Fprintf(os.Stderr, "Unable to encode counters: %s\n", err)
		return
	}

	err = ioutil.WriteFile(CONFIG["countersFile"], b.Bytes(), 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to write data to '%s': %s\n",
			CONFIG["countersFile"], err)
		return
	}
}

func sendMailSMTP(from string, to, cc []string, subject, body string) (errstr string) {
	verbose(3, "Sending email from '%s' to '%s' with subject '%s'...", from, strings.Join(to, ", "), subject)

	msg := []byte(fmt.Sprintf("From: %s\r\n", from) +
		fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")) +
		fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", ")) +
		fmt.Sprintf("Subject: %s\r\n", subject) +
		"X-Slack-Bot: jbot\r\n" +
		"\r\n" +
		body + "\r\n")

	err := smtp.SendMail(CONFIG["SMTP"], nil, from, to, msg)
	if err != nil {
		return fmt.Sprintf("%s", err)
	}

	return
}

func slackChannelPeriodics() {
	verbose(2, "Running slack channel periodics...")
	for _, chInfo := range CHANNELS {
		snowAlert(*chInfo)
		cveAlert(*chInfo)
	}
}

func slackLiveCheck() {
	verbose(2, "Checking if Slack is still sending me messages...")

	threshold := SLACK_LIVE_CHECK * PERIODICS * time.Second

	diff := time.Now().Sub(LAST_SLACK_MESSAGE_TIME)
	if diff.Seconds() > threshold.Seconds() {
		verbose(2, "Uhoh, I haven't seen any messages in %s seconds. Restarting...", threshold)
		serializeData()
		err := syscall.Exec(os.Args[0], os.Args, os.Environ())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to restart: %s\n", err)
		}
	}
}

func slackPeriodics() {
	ticks := PERIODICS * time.Second

	n := 0
	for _ = range time.Tick(ticks) {
		verbose(1, "Running slack periodics...")

		go serializeData()
		go slackChannelPeriodics()

		if (n % SLACK_CHANNEL_UPDATE_INTERVAL) == 0 {
			go updateSlackChannels()
		}
		if (n % CVE_FEED_UPDATE_INTERVAL) == 0 {
			updateCVEData()
		}

		if (n % SLACK_LIVE_CHECK) == 0 {
			slackLiveCheck()
		}
		n++
	}
}

func updateHipChatRooms(rooms []*hipchat.Room) {
	for _, room := range rooms {
		HIPCHAT_ROOMS[room.Id] = room
	}
}

func updateSlackChannels() {
	params := slack.GetConversationsParameters{
		Limit: 1000,
	}

	for {
		channels, cursor, err := SLACK_CLIENT.GetConversations(&params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to get conversations: %s\n", err)
			break
		}
		for _, c := range channels {
			SLACK_CHANNELS[c.Name] = c
		}
		if len(cursor) > 0 {
			params.Cursor = cursor
		} else {
			break
		}
	}
}

func updateRoster(users []*hipchat.User) {
	for _, user := range users {
		uid := strings.Split(user.Id, "@")[0]
		HIPCHAT_ROSTER[uid] = user
	}
}

func updateSeen(r Recipient, msg string) {
	if len(r.Name) == 0 {
		/* Not a chat message. */
		return
	}

	curses_re := regexp.MustCompile(`(shit|motherfucker|piss|f+u+c+k+|cunt|cocksucker|tits)`)
	curses_match := curses_re.FindAllString(msg, -1)

	yubifail_re := regexp.MustCompile(`eiddcc[a-z]{38}`)
	yubifail_match := yubifail_re.FindAllString(msg, -1)

	/* We don't keep track of priv messages, only public groupchat. */
	if ch, chfound := getChannel(r.ChatType, r.ReplyTo); chfound {
		var uInfo UserInfo

		uInfo.Seen = fmt.Sprintf(time.Now().Format(time.UnixDate))
		uInfo.Count = 1
		uInfo.Curses = 0
		uInfo.CurseWords = map[string]int{}
		uInfo.Yubifail = 0
		uInfo.Id = r.Id

		for _, curse := range curses_match {
			incrementCounter("curses", curse)
			incrementCounter("cursers", r.MentionName)
			count, found := uInfo.CurseWords[curse]
			if !found {
				uInfo.CurseWords[curse] = 1
			} else {
				uInfo.CurseWords[curse] = count + 1
			}
		}
		for _ = range yubifail_match {
			incrementCounter("yubifail", r.MentionName)
		}

		count := len(strings.Split(msg, "\n"))
		if count > 1 {
			count -= 1
		}

		if r.ChatType == "hipchat" {
			var u *hipchat.User
			for _, u = range HIPCHAT_ROSTER {
				if u.Name == r.Name {
					break
				}
			}
			if u == nil {
				return
			}

			if t, found := ch.HipChatUsers[*u]; found {
				uInfo.Yubifail = t.Yubifail + len(yubifail_match)
				uInfo.Curses = t.Curses + len(curses_match)
				uInfo.Count = t.Count + count

				/* Need to remember other counters here,
				 * lest they be reset. */
				for c, n := range t.CurseWords {
					uInfo.CurseWords[c] += n
				}
			}
			ch.HipChatUsers[*u] = uInfo
		} else if r.ChatType == "slack" {
			if len(ch.SlackUsers) < 1 {
				ch.SlackUsers = make(map[string]UserInfo, 0)
			}
			if t, found := ch.SlackUsers[r.MentionName]; found {
				uInfo.Yubifail = t.Yubifail + len(yubifail_match)
				uInfo.Curses = t.Curses + len(curses_match)
				uInfo.Count = t.Count + count

				/* Need to remember other counters here,
				 * lest they be reset. */
				for c, n := range t.CurseWords {
					uInfo.CurseWords[c] += n
				}
			}
			ch.SlackUsers[r.MentionName] = uInfo
		}
		CHANNELS[ch.Name] = ch
	}
}

func usage(out io.Writer) {
	usage := `Usage: %v [-DVhv] [-c configFile]
	-D             enable debugging output
	-V             print version information and exit
	-c configFile  read configuration from configFile
	-h             print this help and exit
	-v             be verbose
`
	fmt.Fprintf(out, usage, PROGNAME)
}

func updateChannels() {
	for n, ch := range CHANNELS {
		verbose(2, "Updating channel info for channel %s (#%s)...", n, ch.Name)
		if n != ch.Name {
			fmt.Fprintf(os.Stderr, "+++ dupe: %s (#%s)\n", n, ch.Name)
			delete(CHANNELS, n)
			continue
		}
		for t, v := range TOGGLES {
			if len(ch.Toggles) == 0 {
				ch.Toggles = map[string]bool{}
			}
			if _, found := ch.Toggles[t]; !found {
				ch.Toggles[t] = v
			}
		}

		if ch.Phishy == nil {
			ch.Phishy = &PhishCount{0, 0, time.Now(), time.Unix(0, 0)}
		}

		if ch.CVEs == nil {
			ch.CVEs = map[string]CVEItem{}
		}
	}
}

func verbose(level int, format string, v ...interface{}) {
	if level <= VERBOSITY {
		fmt.Fprintf(os.Stderr, "%s ", time.Now().Format("2006-01-02 15:04:05"))
		for i := 0; i < level; i++ {
			fmt.Fprintf(os.Stderr, "=")
		}
		fmt.Fprintf(os.Stderr, "> "+format+"\n", v...)
	}
}

/*
 * Main
 */

func main() {

	if err := os.Setenv("PATH", "/bin:/usr/bin:/sbin:/usr/sbin:/usr/local/bin"); err != nil {
		fail(fmt.Sprintf("Unable to set PATH: %s\n", err))
	}

	getopts()
	parseConfig()
	createCommands()
	readSavedData()

	defer serializeData()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		<-c
		serializeData()
		os.Exit(EXIT_FAILURE)
	}()

	if len(CONFIG["hcService"]) > 0 {
		doTheHipChat()
	}
	if len(CONFIG["slackService"]) > 0 {
		doTheSlackChat()
	}
	select {}
}
