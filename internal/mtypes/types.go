package mtypes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SendStatus int
const (
	ErrO     SendStatus = -1
	SubOnly  SendStatus = 0
	Skipped  SendStatus = 1
	Sendable SendStatus = 2
)


type (
	Letter struct {
		PageID     string
		UID        uint64
		Title      string
		Newsletters []string
		Markdown   string
		SendAt     string
		SentAt     *time.Time
		Expiry     *time.Time
	}

	Subscriber struct {
		Email string
		Subs  []*Subscription
		Pages []string
	}

	Subscription struct {
		Name string
		ID   string
	}

	EmailContent struct {
		Content     string
		ImgRef      string
		URI         string
                SubNewsURL  string
		Unsubscribe string
	}
)

var sublists []string = []string{ 
	"newsletter",
	"test-news",
	"insider",
	"volunteer",
	"sponsor",
	"speaker",
	"local",
	"genpop",
}

func inSublist(token string) bool {
	for _, list := range sublists {
		if token == list {
			return true
		}
	}
	return false
}

func (l *Letter) ImgRef() string {
	subs := excludeSubs(sublists, l.Newsletters)
	uniqConfs := uniqConfsList(subs)

	var path string
	if len(uniqConfs) == 1 {
		path = uniqConfs[0]
	} else {
		path = "newsletter"
	}
	return fmt.Sprintf("/static/img/%s/logo_blk.svg", path)
}

func findSubset(subs, letters []string) []string {
	set := make([]string, 0)	
	hash := make(map[string]string)

	for _, v := range subs {
		hash[v] = v
	}

	for _, v := range letters {
		if _, ok := hash[v]; ok {
			set = append(set, v)
		}
	}

	return set
}

func excludeSubs(subs, letters []string) []string {
	set := make([]string, 0)	
	hash := make(map[string]string)

	for _, v := range subs {
		hash[v] = v
	}

	for _, v := range letters {
		if _, ok := hash[v]; !ok {
			set = append(set, v)
		}
	}

	return set
}

func uniqConfsList(subs []string) []string {
	hash := make(map[string]string)

	for _, v := range subs {
		key := strings.Split(v,"-")[0]
		if key == "" {
			continue
		}
		if strings.HasPrefix(key, "!") {
			continue
		}
		hash[key] = key
	}

	set := make([]string, len(hash))
	i := 0
	for v := range hash {
		set[i] = v
		i++
	}

	return set
}

/* For btc++, we only allow unsubs for above list */
func (l *Letter) Unsub(sub *Subscriber) string {
	list := l.SubList(sub)
	for _, nl := range list {
		if inSublist(nl) {
			return nl
		}
	}
	return ""
}

func (l *Letter) SubList(sub *Subscriber) []string {
	return findSubset(sub.SubNames(), l.InNewsletters())	
}

/*
The syntax for calc send is:

  - Blank means do not send
  - 'now' means send now
  - 'onsub' means send now, and keep sending when new subs are added
  - A date means send on date at 9am.
    3/4/2025 -> March, 4th 2025 @ 9am
  - +9 -> scheduled for 9-days from now.
    note: '+' days are never sent on weekends!
  - anything else: returns an error
*/
func (l *Letter) CalcSendAt() (time.Time, error) {
	if l.SendAt == "" {
		return time.Now(), fmt.Errorf("Missive %s (%s) is not scheduled", l.Title, l.Missive())
	}

	if l.SendAt == "now" {
		return time.Now(), nil
	}

	if l.SendAt == "onsub" {
		return time.Now(), nil
	}

	if l.SendAt[0:1] == "+" {
		days, err := strconv.Atoi(l.SendAt[1:])
		if err != nil {
			return time.Now(), err
		}

		sendAt := time.Now().AddDate(0, 0, days)
		switch sendAt.Weekday() {
		case time.Sunday:
			days += 1
		case time.Saturday:
			days += 2
		}
		/* Update to new date (we don't send on weekends) */
		sendAt = time.Now().AddDate(0, 0, days)

		/* Set to 8.01a on that day */
		setDate := time.Date(sendAt.Year(), sendAt.Month(), sendAt.Day(), 8, 1, 0, 0, sendAt.Location())
		return setDate, nil
	}

	layout := "1/2/2006"
	pT, err := time.Parse(layout, l.SendAt)
	if err != nil {
		return time.Now(), err
	}
	/* Set time to 8am */
	setDate := time.Date(pT.Year(), pT.Month(), pT.Day(), 8, 1, 0, 0, pT.Location())
	return setDate, nil
}

func (l *Letter) SetSentAt() bool {
	return l.SentAt == nil && l.SendAt == "now" || l.SendAt == "onsub"
}

/* Either it's 'onsub' and already has a 'sent-at' or
 * it's scheduled for a '+X' date */
func (l *Letter) AtSubOnly() bool {
	return (l.SentAt != nil && l.SendAt == "onsub") || (len(l.SendAt) > 0 && l.SendAt[0:1] == "+")
}

func (l *Letter) Scheduled() bool {
	if l.SendAt == "" {
		return false
	}
	if l.SendAt == "now" && l.SentAt != nil {
		return false
	}
	return true
}

func (l *Letter) Sendable() bool {
	return l.Scheduled() && !l.IsExpired()
}

func (l *Letter) IsExpired() bool {
	return l.Expiry != nil && l.Expiry.Before(time.Now())
}

func (l *Letter) InNewsletters() []string {
	nls := make([]string, 0)
	for _, ln := range l.Newsletters {
		if strings.HasPrefix(ln, "!") {
			continue
		}
		nls = append(nls, ln)
	}
	return nls
}

/*
Job identifier for this letter.

	We use the pageID from Notion.
	If you delete the missive from Notion, you won't
	be able to unschedule it.
*/
func (l *Letter) Missive() string {
	return "MISS-" + strconv.FormatUint(l.UID, 10)
}

func (s *Subscriber) AddSublist(subs []string) bool {
	changed := false
	for _, sub := range subs {
		changed = changed || s.AddSubscription(sub)
	}
	return changed
}

/* Returns true if subscribed is new state */
func (s *Subscriber) AddSubscription(name string) bool {
	if s.Subs == nil {
		s.Subs = make([]*Subscription, 0)
	}

	for _, sub := range s.Subs {
		if sub.Name == name {
			return false
		}
	}

	s.Subs = append(s.Subs, &Subscription{
		Name: name,
	})
	return true
}

/* Returns true if unsubscribed is new state */
func (s *Subscriber) RmSubscription(name string) bool {
	if s.Subs == nil {
		return false
	}

	newSubs := make([]*Subscription, 0)
	unsubscribed := false

	for _, sub := range s.Subs {
		if sub.Name == name {
			unsubscribed = true
			continue
		}
		newSubs = append(newSubs, sub)
	}

	s.Subs = newSubs
	return unsubscribed
}

func (s *Subscriber) IsSubscribed(letter *Letter) bool {
	list := findSubset(s.SubNames(), letter.InNewsletters())	
	return len(list) > 0
}

func (s *Subscriber) SubNames() []string {
	var names []string
	for _, sub := range s.Subs {
		names = append(names, sub.Name)
	}
	return names
}
