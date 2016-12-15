package notify

import (
	"os"
	"strconv"
)

// Notification is the standard error struct used in notify
type notification struct {
	code    int
	message string
}

// Error returns the notification text
func (e notification) Error() string {
	return e.message
}

// IsCode checks whether the provided error has the error code %code%.
// errors.error implementations that are not notify.notification are going to be
// given code = 1.
func IsCode(code int, err error) bool {
	if _, ok := err.(notification); ok {
		return code == err.(notification).code
	} else {
		return code == 1
	}
}

// NewNotifier instantiates and returns a new notifier instance (notifier).
// The notification service is started by running notifier.Run()
// If blocking behaviour is required, then Run() should be started normally
// (otherwise as a goroutine). The notification service is stopped by running
// notifier.Exit(). This command will also exit a blocking Run().
//
// Accepted endpoints: string referenes to files (e.g. myservice.log) and
// pointers to implementations of the os.File interface type (e.g. os.Stdout).
// Notes will be sent to all defined endpoints in their specified order.
//
// Other elements of the system can notify the user/write to log by creating and
// using send and fail functions (created by notifier.Sender, notifier.Failure).
// By default these commands will block if the defined capacity of the notes
// channel is too low for the notification stream. The notifier can be made
// non-blocking by instantiating it with flag async=true. This will make all send
// and fail commands non-blocking, but the order of log entries cannot be
// guaranteed, i.e. issuing two sends sequentially can result in reversed log entry
// order. It is thus best to set a higher capacity of the notes channel at instantiation.
//
func NewNotifier(service string, instance string, logAll bool, async bool, notifierCap int, files ...interface{}) *notifier {

	// Initialize bare notifier
	no := notifier{}

	// Prepare endpoints
	if len(files) == 0 {
		syswarn("No endpoints provided. Going to route all notes to os.Stdout")
		files = []interface{}{os.Stdout}
	}

	for i, endpoint := range files {

		switch w := endpoint.(type) {

		case string:
			f, err := openLogFile(w)
			if err == nil || f == os.Stdout {
				no.endpoints.endpointsPtr = append(no.endpoints.endpointsPtr, f)
			}

		case *os.File:
			no.endpoints.endpointsPtr = append(no.endpoints.endpointsPtr, w)

		default:
			syswarn(strconv.Itoa(i+1) + "th endpoint is not supported. Either provide a file path (string) or an instance of *os.File")
		}

	}

	// Set agent details
	noteChan := make(chan *note, notifierCap)
	no.service = service
	no.instance = instance
	no.noteChan = noteChan
	no.logAll = logAll
	no.safetySwitch = false
	no.notificationCodes = standardCodes

	return &no
}

// Sender creates a simplified notify.send function, which requires
// only the value of the message to be passed. Each unique sender (e.g. server,
// client, etc.) should have their own personalized send.
func (no *notifier) Sender(sender string) func(interface{}) error {
	return func(value interface{}) error {
		return send(sender, value, no.noteChan, no.async, &no.ops)
	}
}

// Failure creates a simplified notify.Send(notify.New()) function, which requires
// only the value of the error code and message to be passed.
// Each unique sender (e.g. server, client, etc.) should have their own
// personalized new and/or send.
func (no *notifier) Failure(sender string) func(int, string, ...interface{}) error {
	return func(code int, format string, a ...interface{}) error {
		return send(sender, newf(code, format, a...), no.noteChan, no.async, &no.ops)
	}
}

// SetCodes replaces built-in notification codes with custom ones.
// A partial replacement (e.g. codes 2-10) is also allowed, however only one
// call to notifier.SetCodes is allowed per notifier.
func (no *notifier) SetCodes(newCodes map[int][2]string) error {

	// Sanity check (will panic)
	no.isOK()

	// Fail counter
	fails := 0

	// Only allow one change of codes per notifier
	if no.safetySwitch {
		return no.noteToSelf(newf(999, "You are trying to change notification codes again. This action is not permitted."))
	}

	for code, notification := range newCodes {
		if code <= 1 || code >= 999 {
			no.noteToSelf(newf(4, "Only notification codes 1 < code < 999 are replaceable. Removing '%d'", code))
			delete(newCodes, code)
			fails++
		} else {
			no.notificationCodes[code] = notification
		}
	}

	if fails > 0 {
		return newf(4, "Failed replacing %d status codes: invalid range", fails)
	} else {
		return nil
	}

}

// Run logs and displays messages sent to the Note channel
// Run is the only consumer of the Note channel as well as the logging facility
func (no *notifier) Run() {

	// Sanity check
	no.isOK()

	no.endpoints.Lock()

	var n *note
	var ok bool

	// Receive Notes
runLoop:
	for {

		// Receive or halt notifier operations (send and newf won't work)
		select {
		case n, ok = <-no.noteChan:
			if !ok {
				no.endpoints.Unlock()
				break runLoop
			}
		}

		// Write to endpoints
		switch (*n.Value).(type) {
		case string:
			if no.logAll {
				no.log(n)
			}
		default:
			no.log(n)
		}

	}
}

// Exit closes the note channel and waits a little for the notifier to
func (no *notifier) Exit() {

	// Notify about exiting. Might block until space in the channel is available
	var exitMSG interface{} = "Note channel has been closed. Exiting notifier loop."
	no.noteChan <- &note{"notifier", &exitMSG}

	// Close channel and
	no.ops.Lock()
	no.ops.halt = true
	close(no.noteChan)
	no.ops.Unlock()

	// wait for the backlog to be written
	for len(no.noteChan) > 0 {
		// do nothing
	}

	// Close endpoints
	no.endpoints.Lock()
	for _, endpoint := range no.endpoints.endpointsPtr {
		if endpoint != os.Stdout {
			endpoint.Close()
		}
	}
	no.endpoints.Unlock()

}
