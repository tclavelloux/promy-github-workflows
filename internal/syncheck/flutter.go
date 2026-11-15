package syncheck

const ruleFlutterVersion = "flutter-version"

var _ = register(ruleFlutterVersion)

func (c *checker) checkFlutterRepoFiles() {
	c.checkPinFile(FlutterVersionFile, ruleFlutterVersion, FlutterVersion,
		"the fleet Flutter SDK pin; flutter-lint and flutter-test install the SDK from this file")
}
