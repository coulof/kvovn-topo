# KVOvn-topo demo: follow a two-NIC VM out to its two VPC routers.
# Run from the root of the kvovn-topo repo (the scenario lives in testdata/):
#   vhs demo/kvovn-topo.tape
# Uses the recorded kubevirt-multinic scenario, so it needs no cluster and
# gives the same recording every run.

Output demo/kvovn-topo.webm
Output demo/kvovn-topo.gif

Require kvovn-topo
Set Shell bash
Set FontFamily "Cascadia Code NF"   # has box-drawing and braille glyphs; SUSE Mono has neither
Set FontSize 16
Set Width 1400
Set Height 820
Set Padding 16
Set Theme "Dracula"
Set TypingSpeed 45ms
Set Framerate 30

Hide
Type "export KUBECONFIG=/dev/null && clear" Enter
Sleep 500ms
# 1. Start offline on the recorded Harvester scenario: three VPCs.
#    Launched while hidden so the recording opens on the running TUI.
Type "kvovn-topo --fixture-dir testdata/scenarios/kubevirt-multinic" Enter
Wait+Screen /offline fixture/
Show
Sleep 1s

# 2. Workloads view, select the VM with one NIC in each VPC.
Type "3"
Sleep 1s
Type@120ms "jjjjjj"
Wait+Screen /NICs \(2\)/
Sleep 1s

# 3. First NIC: the details pane shows its path out, switch -> router port -> VPC router.
Type "l"
Sleep 400ms
Type "j"
Wait+Screen /Path out/
Sleep 3s

# 4. Second NIC lands in the other VPC.
Type "l"
Sleep 300ms
Type@150ms "jjj"
Wait+Screen /vpc-twowater/
Sleep 3s

# 5. Follow the subnet relation into the VPC tree, then come back.
Type "l"
Sleep 500ms
Type "j"
Sleep 800ms
Enter
Sleep 3s
Type "b"
Sleep 2s

Type "q"
Sleep 300ms
