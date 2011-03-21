#! /usr/bin/env python
#
# Just a Bunch of Tweets - a twitter bot.
#
# jbot is Beerware:
#
# Originally written by Jan Schaumann <jschauma@netmeister.org> in March 2011.
# As long as you retain this notice you can do whatever you want with this code.
# If we meet some day, and you think jbot's worth it, you can buy me a beer
# in return.

import datetime
import fcntl
import getopt
import os
import random
import re
import sys
import time
import tweepy
import urllib2

###
### Globals
###

EXIT_ERROR = 1
EXIT_SUCCESS = 0

BOTNAME = "j_b_o_t"
BOTOWNER = "jschauma"

MAXCHARS = 140

# http://apiwiki.twitter.com/w/page/22554652/HTTP-Response-Codes-and-Errors
TWITTER_RESPONSE_STATUS = {
        "OK" : 200,
        "NotModified" : 304,
        "RateLimited" : 400,
        "Unauthorized" : 401,
        "Forbidden" : 403,
        "NotFound" : 404,
        "NotAcceptable" : 406,
        "SearchRateLimited" : 420,
        "Broken" : 500,
        "Down" : 502,
        "FailWhale" : 503
    }

NEW = [
        "I know all about Mr.T, Vin Diesel and Chuck Norris. And of course your mom."
    ]

###
### Command methods
###

def cmd_charliesheen(msg, url):
    """Get a quote from Charlie Sheen."""

    try:
        pattern = re.compile('.*<blockquote id="quote">(?P<quote>.*)</blockquote>', re.I)
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('quote')

        sys.stderr.write("Tried to get a quote from %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


def cmd_countdown(msg):
    """Handle a countdown request."""

    txt = msg.text
    pattern = re.compile('.*!countdown (?P<what>.*)')
    match = pattern.match(txt)
    if match:
        what = match.group('what')
        if COUNTDOWNS.has_key(what):
            t1 = time.mktime(time.localtime())
            t2 = COUNTDOWNS[what]
            return "@%s %s" % (msg.user.screen_name, datetime.timedelta(seconds=t2-t1))

    return "%s" % DONTKNOW[random.randint(0,len(DONTKNOW)-1)]


def cmd_feature(msg):
    """Handle a feature request.

    For the most part, this just means printing the given request to
    stdout.
    """

    txt = msg.text
    pattern = re.compile('.*!feature .*')
    match = pattern.match(txt)
    if match:
        print txt

    return "@%s Feature request relayed to my owner. Thank you!" % msg.user.screen_name


def cmd_factlet(msg, url):
    """Get a factlet about a certain personality."""

    pattern = re.compile('.*<summary>(?P<fact>.*)</summary>', re.I)
    try:
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('fact')

        sys.stderr.write("Tried to get a fact from %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


def cmd_help(msg):
    """Return a helpful message."""

    txt = msg.text
    pattern = re.compile('.*!help (?P<command>\S+)')
    match = pattern.match(txt)
    if match:
        command = match.group('command')
        try:
            cmd = COMMANDS[command]
            return "@%s %s" % (msg.user.screen_name, cmd.getHelp())
        except KeyError:
            return cmd_none(msg, command)

    pattern = re.compile('.*!help\s*$')
    match = pattern.match(txt)
    if match:
        return JBOT_HELP_URL

    return "@%s I know of %d commands. Ask me about one of them or see: %s" % \
                (msg.user.screen_name, len(COMMANDS), JBOT_HELP_URL)


def cmd_how(msg):
    """Describe how the given command is implemented."""

    txt = msg.text
    pattern = re.compile('.*!how (?P<command>\S+)')
    match = pattern.match(txt)
    if match:
        command = match.group('command')
        if command == BOTNAME:
            return "@%s Unfortunately, no one can be told what %s is... You have to see it for yourself." % (msg.user.screen_name, BOTNAME)
        try:
            cmd = COMMANDS[command]
            return "@%s %s" % (msg.user.screen_name, cmd.how)
        except KeyError:
            pass

    return "@%s %s" % (msg.user.screen_name, DONTKNOW[random.randint(0,len(DONTKNOW)-1)])


def cmd_insult(msg, url):
    """Insult somebody."""

    txt = msg.text
    pattern = re.compile('.*!insult @?(?P<somebody>\S+)')
    match = pattern.match(txt)
    if match:
        loser = match.group('somebody')
        if ((loser == BOTNAME) or (loser == BOTOWNER)):
            loser = msg.user.screen_name
        try:
            ip = re.compile('.*<font face="Verdana" size="4"><strong><i>(?P<insult>.*)</i>', re.I)
            for line in urllib2.urlopen(url).readlines():
                m = ip.match(line)
                if m:
                    return "@%s %s" % (loser, m.group('insult'))
        except urllib2.URLError, e:
            sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))

        sys.stderr.write("No insult found on %s.\n" % url)

    sys.stderr.write("Entered insult function with no matching message?")


def cmd_new(msg):
    """Explain what's new."""

    return "@%s %s" % (msg.user.screen_name, ",".join(NEW))


def cmd_none(msg, command):
    """Dummy command to return a "no such command" message."""

    return "@%s No such command: %s. Try !help or see: %s" % \
                (msg.user.screen_name, command, JBOT_HELP_URL)


def cmd_schneier(msg, url):
    """Get a Bruce Schneier fact."""

    pattern = re.compile('.*<p class="fact">(?P<fact>.*)</p>', re.I)
    try:
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('fact')

        sys.stderr.write("Tried to get a fact from %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


def cmd_shakespear(msg, url):
    """Generate a shakespearean insult."""

    pattern = re.compile('(?P<insult>.*)</font>', re.I)
    try:
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('insult')

        sys.stderr.write("Tried to get a shakespearean insult %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


def cmd_trivia(msg, url):
    """Get a bit of trivia."""

    pattern = re.compile(".*<div class='factText'>(?P<trivia>.*)</div>", re.I)
    try:
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('trivia')

        sys.stderr.write("Tried to get some trivia from %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


def cmd_tool(msg):
    """Handle a  tool request."""

    txt = msg.text
    pattern = re.compile('.*!tool @?(?P<tool>\S+)')
    match = pattern.match(txt)
    if match:
        tool = match.group('tool')
        return "You're a tool, @%s." % tool


def cmd_yourmom(msg, url):
    """Generate a yo-momma joke."""

    url = "%s/ym%02d.html" % (url, random.randint(1,28))
    pattern = re.compile('(?P<yomomma>.*)<br><br>', re.I)
    try:
        for line in urllib2.urlopen(url).readlines():
            match = pattern.match(line)
            if match:
                return match.group('yomomma')

        sys.stderr.write("Tried to get a yo momma joke from %s but found nothing." % url)
    except urllib2.URLError, e:
        sys.stderr.write("Unable to get %s\n\t%s\n" % (url, e))


###
### Classes
###

class Command(object):
    """An object representing a command.

    A Command can have:
        - a handler -- invoked for this command
        - a usage -- displayed if asked for !help
        - a summary -- displayed if asked for !help
        - a how -- displayed if asked for !how
        - a return type, which may be
            - "None" (any action is handled on the system with no need to
                      return a response to the user)
            - "Tweet" (a response is generated to be returned to the user)
            - "URL" (a response is generated based on the url to be passed
              to the function)
        - a response - possibly generated by the handler
    """

    def __init__(self, name, handler, usage, summary, how, ret):
        """Construct a new command with the given values."""

        assert callable(handler)
        for arg in [ name, usage, summary, how, ret ]:
            assert isinstance(arg, str)

        self.name = name
        self.handler = handler
        self.usage = usage
        self.summary = summary
        self.how = how
        self.ret = ret
        self.response = ""


    def run(self, msg):
        """Run the given command.

        This function will call the object's handler with the given
        message.  It will return either a string to be returned to the
        requestor (if 'ret' is 'Tweet') or None.
        """

        if self.ret == "Tweet":
            return self.handler(msg)
        elif self.ret == "URL":
            return self.handler(msg, self.how)
        elif self.ret == "None":
            self.handler(msg)
            return None


    def getHelp(self):
        """Return a suitable help string."""

        return "!%s %s - %s" % (self.name, self.usage, self.summary)

###
### Bot Globals
###

COMMANDS = {
    "countdown" : Command("countdown", cmd_countdown,
                        "<event>", "display countdown until event",
                        "hardcoded", "Tweet"),
    "feature" : Command("feature", cmd_feature,
                        "<descr>", "request a feature from the author",
                        "message to stdout", "Tweet"),
    "help"    : Command("help", cmd_help,
                        "(<command>)", "request help (about the given command)",
                        "hardcoded", "Tweet"),
    "how"     : Command("how", cmd_how,
                        "(<command>)", "ask how something works",
                        "hardcoded", "Tweet"),
    "insult"  : Command("insult", cmd_insult,
                        "<somebody>", "insult somebody",
                        "http://www.randominsults.net/", "URL"),
    "new"     : Command("new", cmd_new,
                        "", "show what's new",
                        "The Daily Jbot", "Tweet"),
    "tool"    : Command("tool", cmd_tool,
                        "user", "make somebody a tool",
                        "That's a secret.", "Tweet"),
    "trivia"  : Command("trivia", cmd_trivia,
                        "", "display some useless information",
                        "http://www.nicefacts.com/quickfacts/index.php", "URL")
}

JBOT_HELP_URL = "http://www.netmeister.org/apps/twitter/jbot/help.html"

###
### Snarkisms etc.
###

# Things the bot may say if he has no clue about the request.
DONTKNOW = [
        "How the hell am I supposed to know that?",
        "FIIK",
        "ENOCLUE",
        "Buh?",
        "I have no idea.",
        "Sorry, I wouldn't know about that.",
        "I wouldn't tell you even if I knew."
    ]


# Random stuff the bot may say when addressed without a command or regex
# match.
MISC_RESPONSES = [
        "In A.D. 2101, war was beginning.",
        "What happen?",
        "Somebody set up us the bomb.",
        "We get signal.",
        "What!",
        "Main screen turn on.",
        "It's you!",
        "How are you gentlemen!",
        "All your base are belong to us.",
        "You are on the way to destruction.",
        "What you say!",
        "You have no chance to survive make your time.",
        "Captain!",
        "Take off every 'ZIG'!",
        "You know what you doing.",
        "Move 'ZIG'.",
        "For great justice."
    ]

# Things we can count down to.
COUNTDOWNS = {
        "2012" : time.mktime(time.strptime("2012-01-01 00:00:00", "%Y-%m-%d %H:%M:%S")),
        "dst" : time.mktime(time.strptime("2011-11-06 02:00:00", "%Y-%m-%d %H:%M:%S")),
        "eow" : time.mktime(time.strptime("2012-12-21 00:00:00", "%Y-%m-%d %H:%M:%S")),
        "end of the world" : time.mktime(time.strptime("2012-12-21 00:00:00", "%Y-%m-%d %H:%M:%S")),
        "xmas" : time.mktime(time.strptime("2012-12-24 00:00:00", "%Y-%m-%d %H:%M:%S")),
        "festivus" : time.mktime(time.strptime("2012-12-23 00:00:00", "%Y-%m-%d %H:%M:%S")),
        "y2k38" : time.mktime(time.strptime("2038-01-01 03:14:07", "%Y-%m-%d %H:%M:%S")),
        "turkey" : time.mktime(time.strptime("2012-11-24 16:00:00", "%Y-%m-%d %H:%M:%S")),
        "worldcup" : time.mktime(time.strptime("2014-06-13 00:00:00", "%Y-%m-%d %H:%M:%S"))
    }

# If we have a new follower, pick one of these. %user will be replaced
# with the username.
GREETINGS = [
        "Hello %user! I look forward to brightening your day!",
        "I sincerely welcome %user to the list of jbotters.",
        "Yo yo yo, ma homie %user in da house!",
        "Look at that, %user found me! Hooray!",
        "Good day, %user. I hope you will find my services to your liking."
    ]

# If we stop following somebody, pick one of these. %user will be replaced
# with the username.
GOODBYES = [
        "Awww. I'm sad to see you leave, %user. Farewell!",
        "Ooops, I guess I shouldn't have said that about %user.",
        "Smell ya later, %user. (I still can't believe 'Smell ya' later' replaced 'Goodbye'...)",
        "Goodbye, %user. It was nice following you.",
        "It's a sad day - we've lost %user. Oh well, more jbot for the rest of you."
    ]

##
## Regex trigger fall into a number of categories:
##
## function trigger: match an expression and invoke a function
## string trigger  : map a regex to either a single string or a list of
##                   strings
## url trigger     : map a regex to a ( func, url ) tuple, causing the
##                   invocation of the given function with the given url
##

# simple functions triggered by simple regexes
REGEX_FUNC_TRIGGER = {
        # new
        re.compile(".*what's new.*", re.I) : cmd_new
    }

# strings or list of strings triggered by simple regexes
REGEX_STR_TRIGGER = {
        # pirates
        re.compile("(pirate|ahoy|arrr|pillage|yarr|lagoon)", re.I) : [
                "Sing A Chantey!",
                "Bury The Booty!",
                "Take No Prisoners!",
                "Yell 'Land Ho'!",
                "Loot and Pillage!",
                "Swab the Deck!",
                "Guzzle Grog!",
                "Plunder a Sloop!",
                "Sail the High Seas!",
                "Keelhaul a Scurvy Dog!",
                "Raise the Jolly Roger!",
                "Maroon a Scallywag!"
            ],
        # h2g2
        re.compile("(arthur dent|slartibartfast|zaphod|beeblebrox|ford prefect|hoopy|trillian|foolproof|my ego|universe|giveaway|lunchtime|bypass|giveaway|don'?t ?panic|new yorker|deadline|potato|grapefruit|don't remember anything|ancestor|make no sense at all|philosophy|apple products)", re.I) : [
                "If there's anything more important than my ego around here, I want it caught and shot now!",
                "I always said there was something fundamentally wrong with the universe.",
                "Time is an illusion, lunchtime doubly so.",
                "What do you mean, why has it got to be built? It's a bypass. Got to build bypasses.",
                "`Oh dear,' says God, `I hadn't thought of  that,' and promptly vanished in a puff of logic.",
                "It's the first helpful or intelligible thing anybody's said to me all day.",
                "The last time anybody made a list of the top hundred character attributes of New Yorkers, common sense snuck in at number 79.",
                "I love deadlines. I like the whooshing sound they make as they fly by.",
                "It is a mistake to think you can solve any major problem just with potatoes.",
                "Life... is like a grapefruit. It's orange and squishy, and has a few pips in it, and some folks have half a one for breakfast.",
                "Except most of the good bits were about frogs, I remember that.  You would not believe some of the things about frogs.",
                "There was an accident with a contraceptive and a time machine. Now concentrate!",
                "Reality is frequently inaccurate.",
                "It is very easy to be blinded to the essential uselessness of them by the sense of achievement you get from getting them to work at all.",
                "Life: quite interesting in parts, but no substitute for the real thing."
            ],
        # calvin & hobbes
        re.compile("(braindead|retarded|ascertain|calculate|cereal|verbification)", re.I) : [
                "It's psychosomatic. You need a lobotomy. I'll get a saw.",
                "Why waste time learning, when ignorance is instantaneous?",
                "This one's tricky. You have to use imaginary numbers, like eleventeen...",
                "YAAH! DEATH TO OATMEAL!",
                "Verbing weirds language."
            ],
        # seinfeld
        re.compile("(human fund|dog shit|want soup|junior mint|rochelle|aussie|woody allen|puke|mystery wrapped in|marine biologist|sailor|dentist|sophisticated|sleep with me|what do you want to eat)", re.I) : [
                "A Festivus for the rest of us!",
                "If you see two life forms, one of them's making a poop, the other one's carrying it for him, who would you assume is in charge?",
                "No soup for you!  Come back, one year!",
                "It's chocolate, it's peppermint, it's delicious.  It's very refreshing.",
                "A young girl's strange, erotic journey from Milan to Minsk.",
                "Maybe the Dingo ate your baby!",
                "These pretzels are making me thirsty!",
                "'Puke' - that's a funny word.",
                "You're a mystery wrapped in a twinky!",
                "You know I always wanted to pretend that I was an architect!",
                "If I was a woman I'd be down on the dock waiting for the fleet to come in.",
                "Okay, so you were violated by two people while you were under the gas. So what? You're single.",
                "Well, there's nothing more sophisticated than diddling the maid and then chewing some gum.",
                "I'm too tired to even vomit at the thought.",
                "Feels like an Arby's night."
            ],
        # monty python
        re.compile("(camelot|swallow|government|what's wrong|agnostic|really very funny|unexpected|inquisition|romans|say no more|cleese|romanes eunt domus|quod erat|correct latin|hungarian)", re.I) : [
                "On second thought, let's not go to Camelot. It is a silly place.",
                "An African or European swallow?",
                "Strange women lying in ponds distributing swords is no basis for a system of government!",
                "I'll tell you what's wrong with it. It's dead, that's what's wrong with it.",
                "There's nothing an agnostic can't do if he doesn't know whether he believes in anything or not.",
                "I don't think there's a punch-line scheduled, is there?",
                "Nobody expects the Spanish inquisition!",
                "Oehpr Fpuarvre rkcrpgf gur Fcnavfu Vadhvfvgvba.",
                "What have the Romans ever done for us?",
                "Nudge, nudge, wink, wink. Know what I mean?",
                "And now for something completely different.",
                "'People called Romanes they go the house?'",
                "Romani ite domum.",
                "My hovercraft if full of eels."
            ],
        # loveboat
        re.compile("loveboat", re.I) : [
                "Love, exciting and new... Come aboard.  We're expecting you.",
                "Love, life's sweetest reward.  Let it flow, it floats back to you.",
                "The Love Boat, soon will be making another run.",
                "The Love Boat promises something for everyone.",
                "Set a course for adventure, Your mind on a new romance.",
                "Love won't hurt anymore; It's an open smile on a friendly shore."
            ],
        # ninja
        re.compile("(ninja|assassination|on'yomi|oniwaban|shinobi)", re.I) : [
                "Smash something!",
                "Destroy enemy!",
                "Unleash fury!",
                "Stealth attack!",
                "Annihilate adversary!",
                "Jump over a building!",
                "Silence opponent!",
                "Get really mad!",
                "Hypnotize someone!",
                "Escape on a motorcycle!",
                "Strike quickly!",
                "Turn invisible!"
            ],
        # zen of python
        re.compile("(zen of python|TMTOWTDI)", re.I) : [
                "Beautiful is better than ugly.",
                "Explicit is better than implicit.",
                "Simple is better than complex.",
                "Complex is better than complicated.",
                "Flat is better than nested.",
                "Sparse is better than dense.",
                "Readability counts.",
                "Special cases aren't special enough to break the rules.",
                "Although practicality beats purity.",
                "Errors should never pass silently.  Unless explicitly silenced.",
                "In the face of ambiguity, refuse the temptation to guess.",
                "There should be one -- and preferably only one -- obvious way to do it.",
                "Although that way may not be obvious at first unless you're Dutch.",
                "Now is better than never.",
                "Although never is often better than *right* now.",
                "If the implementation is hard to explain, it's a bad idea.",
                "If the implementation is easy to explain, it may be a good idea.",
                "Namespaces are one honking great idea -- let's do more of those!"
            ],
        # hang on
        re.compile("hold on", re.I) : "No, *YOU* hold on!",
        re.compile("hang on", re.I) : "No, *YOU* hang on!",
        # hotness
        re.compile("\b(panties|tied up|underwear|naked|thong|lindsay lohan|unzip|muscle|cowgirl|bikini|paris hilton|strip|underpants|hooker|whore)\b", re.I) : "That's hot.",
        # hollaback
        re.compile("(holl(er|a) ?back|this my shit|b-?a-?n-?a-?n-?a-?s)", re.I) : [
                "Ooooh ooh, this my shit, this my shit.",
                "ain't no hollaback girl.",
                "Let me hear you say this shit is bananas.",
                "B-A-N-A-N-A-S"
            ],
        # milkshake
        re.compile("my milkshake", re.I) : [
                "...brings all the boys to the yard.",
                "The boys are waiting.",
                "Damn right it's better than yours.",
                "I can teach you, but I have to charge.",
                "Warm it up."
            ],
        # Mr. Burns
        re.compile("(outfit|gorilla vest|warm sweater|vampire|rhino|grizzly|noodle|robin|gopher|tuxedo|clogs)", re.I) : [
                "Some men hunt for sport; Others hunt for food; But the only thing I'm hunting for Is an outfit that looks good...",
                "Seeeeeeee my vest! See my vest!  Made from real gorilla chest!",
                "Feel this sweater, There's no better Than authentic Irish setter.",
                "See this hat, 'twas my cat; My evening wear, vampire bat.",
                "These white slippers are albino african endangered rhino.",
                "Grizzly bear underwear; Turtles' necks, I've got my share.",
                "Beret of poodle on my noodle It shall rest.",
                "Try my red robin suit It comes one breast or two.",
                "Like my loafers? Former gophers.  It was that, or skin my chauffeurs.",
                "But a greyhound fur tuxedo would be best.",
                "So lets prepare these dogs; Kill two for matching clogs.",
                "I really like the vest."
            ],
        # Vikings
        re.compile("viking", re.I) : "Spam, lovely Spam, wonderful Spam.",
        # Monkeys
        re.compile("(monkey|orangutan|gorilla|macaque|chimp|\bape\blemur|simian|primate)", re.I) : [
                "Bababooey bababooey bababooey!",
                "Fafa Fooey.",
                "Mama Monkey.",
                "Fla Fla Flo Fly.",
                "Fafa Fooey.",
                "FaFa Fo Hi."
            ]
    }

# Map a regex to a URL function - URL tuple
REGEX_URL_TRIGGER = {
        re.compile("(charlie ?sheen|winning|bree olson|tiger ?blood|warlock)", re.I) :
                        ( cmd_charliesheen, "http://www.livethesheendream.com/" ),
        re.compile("(bruce schneier|crypt|blowfish)", re.I) :
                        ( cmd_schneier, "http://www.schneierfacts.com/" ),
        re.compile(".*(trivia|factual|factlet)", re.I) :
                        ( cmd_trivia, "http://www.nicefacts.com/quickfacts/index.php" ),
        re.compile("(shakespear|hamlet|macbeth|romeo and juliet|merchant of venice|midsummer nicht's dream|henry V|as you like it|All's Well That Ends Well|Comedy of Errors|Cymbeline|Love's Labours Lost|Measure for Measure|Merry Wives of Windsor|Much Ado About Nothing|Pericles|Prince of Tyre|Taming of the Shrew|Tempest|Troilus|Cressida|Twelfth Night|two gentleman of verona|Winter's tale|henry IV|king john|richard II|antony and cleopatra|coriolanus|julius caesar|kind lear|othello|timon of athens|titus|andronicus)", re.I) :
                        ( cmd_shakespear, "http://www.pangloss.com/seidel/Shaker/index.html" ),
        re.compile("(chuck|norris|walker|texas ranger|karate)", re.I) :
                        ( cmd_factlet, "http://4q.cc/index.php?pid=atom&person=chuck" ),
        re.compile("(a-?team|mr(\.? )?t|hannibal|murdock|Baracus)", re.I) :
                        ( cmd_factlet, "http://4q.cc/index.php?pid=atom&person=mrt" ),
        re.compile("(\bvin\b|diesel|fast and (the )?furious|riddick)", re.I) :
                        ( cmd_factlet, "http://4q.cc/index.php?pid=atom&person=vin" ),
        re.compile("(ur([ _])mom|yourmom|m[oa]mma|[^ ]+'s mom)", re.I) :
                        ( cmd_yourmom, "http://www.ahajokes.com" )
    }

###
### The Bot!
###

class Jbot(object):
    """Just a Bunch of Tweets."""

    def __init__(self):
        """Construct a jbot with default values."""

        self.__opts = {
                    "cfg_file" : os.path.expanduser("~/.jbot/config"),
                    "user" : BOTNAME
                 }
        self.api = None
        self.api_credentials = {}
        self.followers = []
        self.friends = []
        self.lastmessage = 0
        self.lmfile = os.path.expanduser("~/.jbot/lastmessage")
        self.lmfd = None
        self.seen = {}
        self.users = {}
        self.verbosity = 0


    class Usage(Exception):
        """A simple exception that provides a usage statement and a return code."""

        def __init__(self, rval):
            self.err = rval
            self.msg = 'Usage: %s [-hv] [-u user]\n' % os.path.basename(sys.argv[0])
            self.msg += '\t-h          print this message and exit\n'
            self.msg += '\t-u user     run as this user\n'
            self.msg += '\t-v          increase verbosity\n'


    def getAccessInfo(self, user):
        """Initialize OAuth Access Info (if not found in the configuration file)."""

        self.auth = tweepy.OAuthHandler(self.api_credentials['key'], self.api_credentials['secret'])

        if self.users.has_key(user):
            return

        auth_url = self.auth.get_authorization_url(True)
        print "Access credentials for %s not found in %s." % (user, self.getOpt("cfg_file"))
        print "Please log in on twitter.com as %s and then go to: " % user
        print "  " + auth_url
        verifier = raw_input("Enter PIN: ").strip()
        self.auth.get_access_token(verifier)

        self.users[user] = {
            "key" : self.auth.access_token.key,
            "secret" : self.auth.access_token.secret
        }

        cfile = self.getOpt("cfg_file")
        try:
            f = file(cfile, "a")
            f.write("%s_key = %s\n" % (user, self.auth.access_token.key))
            f.write("%s_secret = %s\n" % (user, self.auth.access_token.secret))
            f.close()
        except IOError, e:
            sys.stderr.write("Unable to write to config file '%s': %s\n" % \
                (cfile, e.strerror))
            raise


    def getList(self, what, user):
        """Get a full list of things from the API.

        Returns:
            a sorted list of usernames, either followers or 'friends'
        """

        wanted = []

        self.verbose("Getting %s of '%s'." % (what, user), 2)
        if what == "followers":
            func = self.api.followers
        elif what == "friends":
            func = self.api.friends
        else:
            sys.stderr.write("Illegal value '%s' for getList.\n" % what)
            return wanted

        # We only get 100 at a time; our rate limits is 350 calls per
        # hour, and we have to redo the same for 'friends', too, as well
        # as account for various other calls we have to make lateron down
        # the line, so let's do 100 calls only.  This means we can only get
        # at most 10K followers and this tools is thus not useful for really
        # popular accounts, but so be it. Checking the timeout and waiting
        # for that long is unreasonable as well -- for really popular
        # accounts that would mean we wait for days.

        num = 0
        threshold = 100

        try:
            for page in tweepy.Cursor(func).pages():
                wanted.extend([ str(u.screen_name) for u in page ])
                self.verbose("Found %d users (%d in total) from page #%d." % \
                                (len(page), len(wanted), num), 3)
                num = num + 1
                if (num > threshold):
                    self.verbose("Reached my limit of %d users in %d pages. Sorry." % \
                                    (len(wanted), num), 3)
                    break

            wanted.sort()
        except tweepy.error.TweepError, e:
            self.handleTweepError(e, "Unable to get list of %s for %s" % (what, user))

        return wanted


    def getOpt(self, opt):
        """Retrieve the given configuration option.

        Returns:
            The value for the given option if it exists, None otherwise.
        """

        try:
            r = self.__opts[opt]
        except KeyError:
            r = None

        return r


    def getLastMessage(self):
        """Retrieve the last message this bot processed and store it internally.

        This also attempts to get a lock on the file to prevent
        simultaneous instances from running."""

        self.verbose("Trying to get the last processed message...")
        try:
            self.lmfd = file(self.lmfile, "r+")
            fcntl.flock(self.lmfd.fileno(), fcntl.LOCK_EX|fcntl.LOCK_NB)
            for line in self.lmfd.readlines():
                line = line.strip()
                if (line > self.lastmessage):
                    self.lastmessage = line
            # We explicitly do not close the file here; we want to keep
            # the lock on the fd while we're running.
        except IOError, e:
            sys.stderr.write("Unable to open and lock file '%s': %s\n" % \
                                (self.lmfile, e.strerror))
            sys.exit(EXIT_ERROR)

        self.verbose("Last message processed: %s" % self.lastmessage, 2)

        try:
            self.verbose("Determining my own last message...", 2)
            results = self.api.user_timeline(count=1)
            if results:
                mylast = results[0].id
                if (mylast > self.lastmessage):
                    self.lastmessage = results[0].id
            else:
                sys.stderr.write("Unable to find my own last message!\n")
                sys.exit(EXIT_ERROR)
        except tweepy.error.TweepError, e:
            self.handleTweepError(e, "API user_timeline error for %s" % self.getOpt("user"))
            sys.exit(EXIT_ERROR)


    def handleTweepError(self, tweeperr, info):
        """Try to handle a Tweepy Error by bitching about it."""

        diff = 0
        errmsg = ""

        rate_limit = self.api.rate_limit_status()

        if tweeperr and tweeperr.response.status:
            if tweeperr.response.status == TWITTER_RESPONSE_STATUS["FailWhale"]:
                errmsg = "Twitter #FailWhale'd on me on %s.\n" % time.asctime()
            elif tweeperr.response.status == TWITTER_RESPONSE_STATUS["Broken"]:
                errmsg = "Twitter is busted again: %s\n" % time.asctime()
            elif tweeperr.response.status == TWITTER_RESPONSE_STATUS["RateLimited"]:
                errmsg = "Fully rate limited until %s.\n" % rate_limit["reset_time"]
                diff = rate_limit["reset_time_in_seconds"] - time.time()
                sys.stderr.write("Hits left: %d\n" % rate_limit["remaining_hists"])
                if diff > 3500:
                    sys.stderr.write("%d\n%s\n" % (diff,str(rate_limit)))
                    sys.exit(EXIT_ERROR)
            elif tweeperr.response.status == TWITTER_RESPONSE_STATUS["SearchRateLimited"]:
                errmsg = "SearchRate limited until %s.\n" % rate_limit["reset_time"]
                diff = rate_limit["reset_time_in_seconds"] - time.time()
                sys.stderr.write("Hits left: %d\n" % rate_limit["remaining_hists"])
                if diff > 3500:
                    sys.stderr.write("%d\n%s\n" % (diff,str(rate_limit)))
                    sys.exit(EXIT_ERROR)
            else:
                errmsg = "On %s Twitter told me:\n'%s'\n" % (time.asctime(), tweeperr)

        sys.stderr.write(info + "\n" + errmsg)

        if diff:
            sys.stderr.write("Sleeping for %d seconds...\n" % diff)
            time.sleep(diff)


    def followOrUnfollow(self, action, users):
        """Start to follow the given list of users."""

        self.verbose("Now %sing: %s" % (action, ",".join(users)), 2)
        for u in users:
            self.verbose("Now %sing %s...", u)
            try:
                if action == "follow":
                    reply = GREETINGS[random.randint(0,len(GREETINGS)-1)]
                    reply = re.sub(r'%user', u, reply)
                    self.tweet(reply)
                    self.api.create_friendship(screen_name=u)
                elif action == "unfollow":
                    reply = GOODBYES[random.randint(0,len(GOODBYES)-1)]
                    reply = re.sub(r'%user', u, reply)
                    self.tweet(reply)
                    self.api.destroy_friendship(screen_name=u)
                else:
                    sys.stderr.write("Illegal action for 'followOrUnfollow': %s\n" % action)
                    sys.exit(EXIT_ERROR)
            except tweepy.error.TweepError, e:
                self.handleTweepError(e, "API error %sing %s" % (action, u))


    def parseConfig(self, cfile):
        """Parse the configuration file and set appropriate variables.

        This function may throw an exception if it can't read or parse the
        configuration file (for any reason).

        Arguments:
            cfile -- the configuration file to parse

        Aborts:
            if we can't access the config file
        """

        try:
            f = file(cfile)
        except IOError, e:
            sys.stderr.write("Unable to open config file '%s': %s\n" % \
                (cfile, e.strerror))
            sys.exit(EXIT_ERROR)

        key_pattern = re.compile('^(?P<username>[^#]+)_key\s*=\s*(?P<key>.+)')
        secret_pattern = re.compile('^(?P<username>[^#]+)_secret\s*=\s*(?P<secret>.+)')
        for line in f.readlines():
            line = line.strip()
            key_match = key_pattern.match(line)
            if key_match:
                user = key_match.group('username')
                if user == "<api>":
                    self.api_credentials['key'] = key_match.group('key')
                else:
                    if self.users.has_key(user):
                        self.users[user]['key'] = key_match.group('key')
                    else:
                        self.users[user] = {
                            "key" : key_match.group('key')
                        }

            secret_match = secret_pattern.match(line)
            if secret_match:
                user = secret_match.group('username')
                if user == "<api>":
                    self.api_credentials['secret'] = secret_match.group('secret')
                else:
                    if self.users.has_key(user):
                        self.users[user]['secret'] = secret_match.group('secret')
                    else:
                        self.users[user] = {
                            "secret" : secret_match.group('secret')
                        }


    def parseOptions(self, inargs):
        """Parse given command-line options and set appropriate attributes.

        Arguments:
            inargs -- arguments to parse

        Raises:
            Usage -- if '-h' or invalid command-line args are given
        """

        try:
            opts, args = getopt.getopt(inargs, "hu:v")
        except getopt.GetoptError:
            raise self.Usage(EXIT_ERROR)

        for o, a in opts:
            if o in ("-h"):
                raise self.Usage(EXIT_SUCCESS)
            if o in ("-u"):
                self.setOpt("user", a)
            if o in ("-v"):
                self.verbosity = self.verbosity + 1

        if args:
            raise self.Usage(EXIT_ERROR)


    def processAtMessages(self):
        """Process all messages to this bot.

        This function will search for all at-messages for this bot (since
        the last time the bot ran) and process them accordingly.
        """

        self.verbose("Processing at-messages...")
        try:
            results = self.api.mentions(since_id=self.lastmessage)
            for msg in results:
                if not self.processMessage(msg):
                    # XXX: this needs to go into a function somewhere else
                    # instead of being crammed in here
                    ip = re.compile("(damm?n? you|shut ?up|die|(cram|stuff) it|piss ?off|(fuck|screw|hate) you|stupid|you (stink|blow)|go to hell|stfu|idiot|(you are|is) annoying|down boy)", re.I)
                    m = ip.match(msg.text)
                    if m:
                        self.tweet(cmd_insult("!insult %s" % msg.user.screen_name))
                    else:
                        self.tweet("@%s %s" % (msg.user.screen_name,
                                        MISC_RESPONSES[random.randint(0,len(MISC_RESPONSES)-1)]))
        except tweepy.error.TweepError, e:
            self.handleTweepError(e, "API mentions error for myself.")


    def processCommands(self, msg):
        """Process the given message by looking for and responding to
        commands.

        Returns true if it found any, false otherwise."""

        txt = msg.text
        pattern = re.compile('.*@%s !(?P<command>\S+).*' % self.getOpt("user"))
        match = pattern.match(txt)
        if match:
            response = ""
            command = match.group('command')
            self.verbose("Found command %s..." % command, 4)
            try:
                cmd = COMMANDS[command]
                response = cmd.run(msg)
            except KeyError:
                response = cmd_none(msg, command)

            if response:
                self.tweet(response)
            else:
                sys.stderr.write("Ran %s but got nothing back...\n" % command)

            return True

        return False


    def processFuncTrigger(self, msg):
        """Process the given message looking for specific function
        trigger.

        Returns true if it found anything, false otherwise."""

        self.verbose("Processing func regexes in %d from %s..." % (msg.id, msg.user.screen_name), 5)
        txt = msg.text
        for pattern in REGEX_FUNC_TRIGGER.keys():
            match = pattern.search(txt)
            if match:
                func = REGEX_FUNC_TRIGGER[pattern]
                if callable(func):
                    response = func(msg)
                    if response:
                        self.tweet("@%s %s" % (msg.user.screen_name, response))
                        return True
                    else:
                        sys.stderr.write("Called %s but got nothing..." % func.__name__)
                else:
                    sys.stderr.write("Unable to call %s?" % func.__name__)

        return False


    def processFollowerMessages(self):
        """Process all messages from this bots followers.

        This function will get all the messages from all users following
        this bot (since the last time the bot ran) and process them
        accordingly.
        """

        self.verbose("Processing all of my followers messages...")
        for friend in self.friends:
            self.verbose("Processing messages from %s (newer than %s)..." % (friend, self.lastmessage), 2)
            try:
                results = self.api.user_timeline(screen_name=friend,since_id=self.lastmessage)
                for msg in results:
                    self.processMessage(msg)
            except tweepy.error.TweepError, e:
                self.handleTweepError(e, "API friends_timeline error for %s" % friend)


    def processMessage(self, msg):
        """Process a single message.

        Given a message, look for the string "@j_b_o_t !command args"; if
        that matches, execute the given command.  If it does not match,
        look for any additional 'eastereggs' (free pattern matches,
        amusing as they are).

        Returns True if a response was sent, False otherwise.
        """

        if self.seen.has_key(msg.id):
            self.verbose("Skipping message %d from %s (already seen)..." % \
                            (msg.id, msg.user.screen_name), 3)
            return True

        self.seen[msg.id] = 1

        self.verbose("Processing message %d from %s..." % (msg.id, msg.user.screen_name), 3)
        if self.processCommands(msg):
            return True

        if self.processRegexes(msg):
            return True

        return False


    def processRegexes(self, msg):
        """Process the given message by looking for any regexes.

        Returns true if it found any, false otherwise."""

        self.verbose("Processing regexes in %d from %s..." % (msg.id, msg.user.screen_name), 4)

        if self.processStrTrigger(msg):
            return True

        if self.processFuncTrigger(msg):
            return True

        if self.processUrlTrigger(msg):
            return True

        return False


    def processStrTrigger(self, msg):
        """Process the given message looking for specific string
        trigger.

        Returns true if it found anything, false otherwise."""

        self.verbose("Processing str regexes in %d from %s..." % (msg.id, msg.user.screen_name), 5)
        txt = msg.text
        for pattern in REGEX_STR_TRIGGER.keys():
            match = pattern.search(txt)
            if match:
                response = REGEX_STR_TRIGGER[pattern]
                if isinstance(response, str):
                    self.tweet("@%s %s" % msg.user.screen_name, response)
                    return True
                if isinstance(response, list):
                    self.tweet("@%s %s" % (msg.user.screen_name,
                                            response[random.randint(0,len(response)-1)]))
                    return True

        return False



    def processUrlTrigger(self, msg):
        """Process the given message looking for specific URL trigger.

        Returns true if it found anything, false otherwise."""

        self.verbose("Processing url regexes in %d from %s..." % (msg.id, msg.user.screen_name), 5)
        txt = msg.text
        for pattern in REGEX_URL_TRIGGER.keys():
            match = pattern.search(txt)
            if match:
                (func, link) = REGEX_URL_TRIGGER[pattern]
                if callable(func):
                    response = func(msg, link)
                    if response:
                        self.tweet("@%s %s" % (msg.user.screen_name, response))
                        return True
                    else:
                        sys.stderr.write("Called %s but got nothing..." % func.__name__)
                else:
                    sys.stderr.write("Unable to call %s?" % func.__name__)

        return False


    def setOpt(self, opt, val):
        """Set the given option to the provided value"""

        self.__opts[opt] = val


    def setupApi(self, user):
        """Create the object's api"""

        key = self.users[user]["key"]
        secret = self.users[user]["secret"]
        self.auth.set_access_token(key, secret)

        self.api = tweepy.API(self.auth)


    def tweet(self, msg):
        """Tweet the given message.

        If the message is too long, it will be truncated.
        """

        self.verbose("Tweeting: %s" % msg, 2)

        if len(msg) > MAXCHARS:
            msg = ' '.join(msg[:136].split(' ')[0:-1]) + '...'

        try:
            self.api.update_status(msg)
        except tweepy.error.TweepError, e:
            sys.stderr.write("Unable to tweet '%s': %s\n" % (msg, e))


    def updateFollowship(self):
        """Find people following this bot and follow them, stop following
        those that stopped following us."""

        self.verbose("Updating followship...")
        user = self.getOpt("user")
        self.followers = self.getList("followers", user)
        self.friends = self.getList("friends", user)

        if not self.friends or (len(self.friends) == 0) or \
            not self.followers or (len(self.followers) == 0):
            sys.stderr.write("Failed to get followship. Pretending nothing changed.")
            return

        new_followers = list(set.difference(set(self.followers), set(self.friends)))

        if len(new_followers):
            self.followOrUnfollow("follow", new_followers)

        gone_followers = list(set.difference(set(self.friends), set(self.followers)))
        if len(gone_followers):
            if len(gone_followers) == len(self.followers):
                sys.stderr.write("All followers gone?\n")
                sys.exit(EXIT_ERROR)

            self.followOrUnfollow("unfollow", gone_followers)

        # At the end of the day, we should have identical membership
        # between those we follow and those that follow us.
        self.followers = list(set.intersection(
                                set.union(set(new_followers), set(self.followers)),
                                set(self.friends)))
        self.friends = self.followers


    def updateLastMessage(self):
        """Write out the message ID of the last message we've processed."""

        msgs = self.seen.keys()
        if msgs:
            msgs.sort()
            self.lastmessage = msgs.pop()

        self.verbose("Updating last-run timestamp...")
        try:
            # We still have an open file handle with a lock from when we
            # read our last message, so just rewind, write and then close
            # (and release the lock).
            self.lmfd.seek(0)
            self.lmfd.write("%s\n" % self.lastmessage)
            self.lmfd.close()
        except IOError, e:
            sys.stderr.write("Unable to write to '%s': %s\n" % \
                                            (self.lmfile, e.strerror))
            raise


    def verbose(self, msg, level=1):
        """Print given message to STDERR if the object's verbosity is >=
           the given level"""

        if (self.verbosity >= level):
            sys.stderr.write("%s> %s\n" % ('=' * level, msg))


    def verifyConfig(self):
        """Verify that we have api credentials."""

        if (not (self.api_credentials.has_key("key") and self.api_credentials.has_key("secret"))):
            sys.stderr.write("No API credentials found.  Please do the 'register-this-app' dance.\n")
            sys.exit(EXIT_ERROR)


###
### "Main"
###

if __name__ == "__main__":
    try:
        jbot = Jbot()
        try:
            jbot.parseOptions(sys.argv[1:])
            jbot.parseConfig(jbot.getOpt("cfg_file"))
            jbot.verifyConfig()

            jbot.getAccessInfo(jbot.getOpt("user"))
            jbot.setupApi(jbot.getOpt("user"))

            jbot.getLastMessage()
            jbot.updateFollowship()
            jbot.processFollowerMessages()
            jbot.processAtMessages()
            jbot.updateLastMessage()

        except jbot.Usage, u:
            if (u.err == EXIT_ERROR):
                out = sys.stderr
            else:
                out = sys.stdout
            out.write(u.msg)
            sys.exit(u.err)
	        # NOTREACHED

    except KeyboardInterrupt:
        # catch ^C, so we don't get a "confusing" python trace
        sys.exit(EXIT_ERROR)
