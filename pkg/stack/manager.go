package stack

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

type Manager struct {
	lock        sync.Mutex
	stacks      []*stack
	ts          string
	rtm         *slack.RTM
	cfEndpoint  string
	channel     string
	slackHeader string
}

func (s *Manager) addStack(name string, stack *stack) {
	s.lock.Lock()
	defer s.lock.Unlock()

	stack.id = len(s.stacks)
	s.stacks = append(s.stacks, stack)
}

func (s *Manager) getStack(name string) *stack {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, stack := range s.stacks {
		if stack.name == name {
			return stack
		}
	}
	return nil
}

type status string

const (
	working status = "working"
	skipped status = "skipped"
	done    status = "done"
	failed  status = "failed"
)

type stack struct {
	id     int
	name   string
	create bool
	start  time.Time
	end    time.Time
	status status
}

type describeResponse struct {
	DescribeStacksResponse struct {
		Stacks struct {
			Member struct {
				StackStatus string
			}
		}
	}
}

type ManagerParams struct {
	SlackToken             string // xoxb-asdfasfasdfasd
	SlackChannel           string // like CUL812373
	SlackHeader            string // like "Deploying to production https://github.com/actions/blah
	CloudformationEndpoint string // like cloudformation.us-east-1.amazonaws.com
}

func NewManager(mp ManagerParams) *Manager {
	api := slack.New(
		mp.SlackToken,
	)

	rtm := api.NewRTM()
	go rtm.ManageConnection()

	mgr := Manager{
		stacks:      make([]*stack, 0),
		rtm:         rtm,
		cfEndpoint:  mp.CloudformationEndpoint,
		slackHeader: mp.SlackHeader,
		channel:     mp.SlackChannel,
	}
	go mgr.handleMessages()

	return &mgr
}

func stackFromPayload(payload string) *stack {
	pairs := strings.Split(string(payload), "&")
	s := stack{
		status: working,
		start:  time.Now(),
	}
	for _, pair := range pairs {
		split := strings.Split(pair, "=")
		if split[0] == "StackName" {
			s.name = split[1]
		} else if split[0] == "ChangeSetType" {
			s.create = split[1] == "CREATE"
		}
	}
	return &s
}

func (s *stack) updateFromDescribeResponse(body []byte) {
	reply := describeResponse{}
	err := xml.Unmarshal(body, &reply)
	if err != nil {
		log.WithError(err).Info("can't unmarshal aws reply; dying")
		panic(err)
	}
	status := reply.DescribeStacksResponse.Stacks.Member.StackStatus
	if status == "CREATE_COMPLETE" || status == "DELETE_COMPLETE" {
		s.status = done
	} else if strings.Contains(status, "FAILED") || strings.Contains(status, "ROLLBACK") {
		s.status = failed
	}
}

func (m *Manager) HandleHTTP(w http.ResponseWriter, req *http.Request) {
	req.URL.Scheme = "https"

	req.URL.Host = m.cfEndpoint

	log.Debug("about to make round trip req: ", req.URL.Scheme, " ", req.URL, req.URL.Path)

	payload, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.WithError(err).Info("can't read payload")
		panic(err)
	}

	s := stackFromPayload(string(payload))
	if strings.Contains(string(payload), "Action=CreateChangeSet&") {
		m.addStack(string(payload), s)
		m.Broadcast()
	} else if strings.Contains(string(payload), "Action=DescribeStacks&") {
		s = m.getStack(s.name)
	}

	log.Debug("payload: ", string(payload))
	req.Body = ioutil.NopCloser(bytes.NewReader(payload))

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	reply, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.WithError(err).Info("can't read payload")
		panic(err)
	}

	log.Debug("reply: ", string(reply))

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// This is a little tricky, because you only want to start updating the stack when
	// the awscli has started causing CF to do stuff.
	if s != nil && strings.Contains(string(payload), "Action=DescribeStacks&") {
		if s.status == skipped {
			_, err = io.Copy(w, s.shortCircuit(reply))
			if err != nil {
				log.WithError(err).Error("couldn't write reply to client")
			}
			return
		}

		s.updateFromDescribeResponse(reply)
		m.Broadcast()
	}

	_, err = io.Copy(w, bytes.NewReader(reply))
	if err != nil {
		log.WithError(err).Error("couldn't write reply to client")
	}

	log.Debug("done handling request\n\n")
}

func (s *stack) shortCircuit(body []byte) io.Reader {
	re := regexp.MustCompile("<StackStatus>.*</StackStatus>")
	injected := "<StackStatus>UPDATE_COMPLETE</StackStatus>"
	if s.create {
		injected = "<StackStatus>CREATE_COMPLETE</StackStatus>"
	}
	return strings.NewReader(string(re.ReplaceAll(body, []byte(injected))))
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

var colorMap = map[status]string{
	skipped: "#aaa",
	working: "#ffa500",
	failed:  "#ff4500",
	done:    "#0b0",
}

func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func (s *stack) statusString() string {
	status := ""
	switch s.status {
	case working:
		duration := time.Since(s.start)
		status = fmt.Sprintf(" deploying (%s)", formatDuration(duration))
	case skipped:
		status = " skipped"
	case done:
		duration := s.end.Sub(s.start)
		status = fmt.Sprintf(" succeeded (took %s)", formatDuration(duration))
	case failed:
		status = " FAILED"
	}

	return s.name + status
}

func (m *Manager) Skip(name string) {
	s := m.getStack(name)
	s.status = skipped
}

func (m *Manager) makeSlackAttachments() []slack.Attachment {
	m.lock.Lock()
	defer m.lock.Unlock()

	blocks := make([]slack.Attachment, len(m.stacks))

	for i, stack := range m.stacks {
		color := colorMap[stack.status]
		blocks[i] = slack.Attachment{Color: color, Text: stack.statusString(), ID: stack.id}
	}

	return blocks
}

func (m *Manager) Broadcast() {
	blocks := m.makeSlackAttachments()

	header := slack.MsgOptionText(m.slackHeader, false)

	if m.ts == "" {
		_, s2, err := m.rtm.PostMessage(m.channel, header, slack.MsgOptionAttachments(blocks...))
		m.ts = s2
		if err != nil {
			log.WithError(err).Error("couldn't post message to slack")
		}
	} else {
		_, _, _, err := m.rtm.UpdateMessage(m.channel, m.ts, header, slack.MsgOptionAttachments(blocks...))
		if err != nil {
			log.WithError(err).Error("couldn't update slack message")
		}
	}
}

func (m *Manager) handleMessages() {
	for msg := range m.rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.HelloEvent:
			ch, ts, err := m.rtm.PostMessage(m.channel, slack.MsgOptionText("let's do this", false))
			log.WithFields(log.Fields{"ch": ch, "ts": ts, "err": err}).Debug("hello message details")

		case *slack.ConnectedEvent:
			log.Info("connected; info: ", ev.Info)

		case *slack.MessageEvent:
			log.Trace("Message: %v (%s)\n", ev, ev.Channel)
			if ev.ThreadTimestamp == m.ts {
				// time to skip shit
				split := strings.SplitN(ev.Text, " ", 2)
				if len(split) == 2 && split[0] == "skip" {
					m.Skip(strings.TrimSpace(split[1]))
					m.rtm.PostMessage(m.channel, slack.MsgOptionText("skipping "+split[1], false), slack.MsgOptionTS(m.ts))
				}
			}

		case *slack.RTMError:
			log.Error("RTM Error: ", ev.Error())

		case *slack.InvalidAuthEvent:
			log.Error("Invalid credentials")
			return

		default:
			// Ignore other events..
			// fmt.Printf("Unexpected: %v\n", msg.Data)
		}
	}
}
