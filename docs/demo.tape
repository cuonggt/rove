# Records docs/demo.gif.
#
#   test/fixtures/sshd/up.sh     # six sshd containers
#   docs/demo-setup.sh           # names them like real servers
#   go build -o rove ./cmd/rove
#   vhs docs/demo.tape
#
# The hosts are local containers with plausible names, so the recording
# shows the real interface without putting anyone's infrastructure in a gif.

Output docs/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1320
Set Height 420
Set Padding 26
Set TypingSpeed 55ms
Set Framerate 24

# Start from a clean prompt rather than recording the shell setup.
Hide
Type "export PS1='$ ' && clear"
Enter
Show

Sleep 700ms
Type "rove"
Sleep 400ms
Hide
Backspace 4
Type "./rove -config docs/demo.config"
Show
Enter

# The fleet fills in host by host as answers arrive.
Sleep 4s

# CPU is a delta between two samples, so a demo shorter than the refresh
# interval would show an empty column throughout. Ask for the second sample.
Type "r"
Sleep 3s

# Walk to a host that needs attention.
Down
Sleep 600ms
Down
Sleep 900ms

# The overview card: what is going on with one machine, without a shell.
Enter
Sleep 3500ms

# Services, with the failed unit sorted to the top where it belongs.
Type "v"
Sleep 3500ms

# Processes, sorted by CPU on the host that can sort.
Type "p"
Sleep 3s

# Disk, straight from the snapshot with no extra round trip.
Type "d"
Sleep 2500ms

Escape
Sleep 800ms

# Filter by name, environment or tag.
Type "/"
Sleep 500ms
Type "prod"
Sleep 1500ms
Enter
Sleep 2500ms

Escape
Sleep 1200ms

Type "q"
Sleep 1s
