import UIKit
import XCTest

// The weaker level: the extension's controller, compiled into this test bundle and hosted in the app, typing into
// a fake text document proxy. Buttons get their actions sent directly; nothing goes through the system keyboard
// machinery, so this proves the key logic, not that iOS runs the extension.
final class FakeTextDocumentProxy: NSObject, UITextDocumentProxy {
    var text = ""

    var documentContextBeforeInput: String? { text }
    var documentContextAfterInput: String? { "" }
    var selectedText: String? { nil }
    var documentInputMode: UITextInputMode? { nil }
    var documentIdentifier: UUID = UUID()
    var hasText: Bool { !text.isEmpty }

    func adjustTextPosition(byCharacterOffset offset: Int) {}
    func setMarkedText(_ markedText: String, selectedRange: NSRange) {}
    func unmarkText() {}

    func insertText(_ newText: String) {
        text += newText
    }

    func deleteBackward() {
        if !text.isEmpty {
            text.removeLast()
        }
    }
}

final class HostedKeyboardController: KeyboardViewController {
    let fake = FakeTextDocumentProxy()

    override var textDocumentProxy: UITextDocumentProxy { fake }
}

final class HostedKeyboardTests: XCTestCase {
    @MainActor
    func testEveryCaseWithFakeProxy() throws {
        let cases = try CheckCases.load(from: Bundle(for: HostedKeyboardTests.self))
        XCTAssertFalse(cases.isEmpty, "expect.tsv has no case")
        for item in cases {
            let controller = HostedKeyboardController()
            controller.loadViewIfNeeded()
            controller.view.frame = CGRect(x: 0, y: 0, width: 390, height: 260)
            controller.view.layoutIfNeeded()
            var problem: String?
            for step in item.steps {
                if step.hasPrefix("long:") {
                    let parts = step.dropFirst(5).split(separator: "=").map(String.init)
                    guard parts.count == 2 else { problem = "bad step \(step)"; break }
                    controller.showChoices(forKeyID: parts[0])
                    guard let choice = Self.button(parts[1], in: controller.view) else {
                        problem = "choice \(parts[1]) not shown"
                        break
                    }
                    choice.sendActions(for: .touchUpInside)
                } else {
                    guard let key = Self.button(step, in: controller.view) else {
                        problem = "key \(step) not found"
                        break
                    }
                    key.sendActions(for: .touchUpInside)
                }
            }
            let got = controller.fake.text
            let pass = problem == nil && got == item.expected
            var detail = "got \(CheckCases.codePoints(got)), want \(CheckCases.codePoints(item.expected))"
            if let problem {
                detail = problem + "; " + detail
            }
            CheckCases.report(pass, "unit-weaker:\(item.name)", detail)
            XCTAssertTrue(pass, "\(item.name): \(detail)")
        }
    }

    private static func button(_ id: String, in view: UIView) -> UIButton? {
        if let button = view as? UIButton, button.accessibilityIdentifier == id {
            return button
        }
        for sub in view.subviews {
            if let found = button(id, in: sub) {
                return found
            }
        }
        return nil
    }
}
