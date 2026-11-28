import XCTest

// Types every case of expect.tsv with the real keyboard extension: taps (touch events) at the keys' places, reads
// the host app's text field back. One test method and one app launch: after the first launch in a boot the
// simulator can connect the Mac's hardware keyboard, which hides software keyboards for later launches.
final class TypingUITests: XCTestCase {
    private let nextKeyboardLabels = ["Next keyboard", "Next Keyboard", "Globe", "Emoji", "YAKBD Check"]

    @MainActor
    func testTypeEveryCase() throws {
        continueAfterFailure = true
        let cases = try CheckCases.load(from: Bundle(for: TypingUITests.self))
        guard let firstStep = cases.first?.steps.first else {
            XCTFail("expect.tsv has no case")
            return
        }
        // The key the first case starts with shows that the check keyboard is up.
        let probeID = Self.keyID(of: firstStep)

        let app = XCUIApplication()
        app.launch()
        let field = app.textFields["check-field"]
        guard field.waitForExistence(timeout: 30) else {
            CheckCases.report(false, "reach", "host app text field not found")
            XCTFail("no text field")
            return
        }
        field.tap()

        let probe = app.buttons[probeID]
        var switches = 0
        if !probe.waitForExistence(timeout: 20) {
            for _ in 0..<6 where !probe.exists {
                let next = app.buttons.matching(NSPredicate(format: "label IN %@", nextKeyboardLabels)).firstMatch
                guard next.waitForExistence(timeout: 10) else { break }
                next.tap()
                switches += 1
                _ = probe.waitForExistence(timeout: 10)
            }
        }
        guard probe.exists else {
            CheckCases.report(false, "reach", "key \(probeID) never appeared after \(switches) keyboard switches; "
                + "software keyboard shown: \(app.keyboards.firstMatch.exists)")
            print(app.debugDescription)
            attachScreenshot("not-reached")
            XCTFail("check keyboard not reached")
            return
        }
        CheckCases.report(true, "reach", "after \(switches) keyboard switches")
        attachScreenshot("reached")

        let clear = app.buttons["clear-button"]
        for item in cases {
            clear.tap()
            var problem: String?
            for step in item.steps {
                if let why = perform(step, in: app) {
                    problem = why
                    break
                }
            }
            var got = (field.value as? String) ?? ""
            let deadline = Date().addingTimeInterval(3)
            while got != item.expected && problem == nil && Date() < deadline {
                Thread.sleep(forTimeInterval: 0.2)
                got = (field.value as? String) ?? ""
            }
            let pass = problem == nil && got == item.expected
            var detail = "got \(CheckCases.codePoints(got)), want \(CheckCases.codePoints(item.expected))"
            if let problem {
                detail = problem + "; " + detail
            }
            CheckCases.report(pass, "ui:\(item.name)", detail)
            if !pass {
                attachScreenshot("fail-\(item.name)")
            }
            XCTAssertTrue(pass, "\(item.name): \(detail)")
        }
    }

    private static func keyID(of step: String) -> String {
        guard step.hasPrefix("long:") else { return step }
        return String(step.dropFirst(5).split(separator: "=").first ?? "")
    }

    // Returns why the step could not be done, or nil.
    @MainActor
    private func perform(_ step: String, in app: XCUIApplication) -> String? {
        if step.hasPrefix("long:") {
            let parts = step.dropFirst(5).split(separator: "=").map(String.init)
            guard parts.count == 2 else { return "bad step \(step)" }
            let key = app.buttons[parts[0]]
            guard key.waitForExistence(timeout: 5) else { return "key \(parts[0]) not found" }
            key.press(forDuration: 1.0)
            let choice = app.buttons[parts[1]]
            guard choice.waitForExistence(timeout: 5) else {
                print(app.debugDescription)
                return "choice \(parts[1]) not shown after holding \(parts[0])"
            }
            choice.tap()
            return nil
        }
        let key = app.buttons[step]
        guard key.waitForExistence(timeout: 5) else {
            print(app.debugDescription)
            return "key \(step) not found"
        }
        key.tap()
        return nil
    }

    @MainActor
    private func attachScreenshot(_ name: String) {
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
