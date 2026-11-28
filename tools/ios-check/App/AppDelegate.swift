import UIKit

// The host app of the check keyboard: one text field the UI test types into, and a button that empties it
// between cases without touching the keyboard.
@main
final class AppDelegate: UIResponder, UIApplicationDelegate {
    var window: UIWindow?

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        let window = UIWindow(frame: UIScreen.main.bounds)
        window.rootViewController = HostViewController()
        window.makeKeyAndVisible()
        self.window = window
        return true
    }
}

final class HostViewController: UIViewController {
    private let field = UITextField()

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground

        let note = UILabel()
        note.text = "YAKBD keyboard check"
        note.accessibilityIdentifier = "check-title"

        // No placeholder: an empty field's value would read back as the placeholder.
        field.accessibilityIdentifier = "check-field"
        field.borderStyle = .roundedRect
        field.autocorrectionType = .no
        field.autocapitalizationType = .none
        field.spellCheckingType = .no
        field.smartQuotesType = .no
        field.smartDashesType = .no
        field.smartInsertDeleteType = .no

        let clear = UIButton(type: .system)
        clear.setTitle("Clear", for: .normal)
        clear.accessibilityIdentifier = "clear-button"
        clear.addTarget(self, action: #selector(clearField), for: .touchUpInside)

        let stack = UIStackView(arrangedSubviews: [note, field, clear])
        stack.axis = .vertical
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor, constant: 20),
            stack.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor, constant: -20),
            stack.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 20),
            field.heightAnchor.constraint(equalToConstant: 44),
        ])
    }

    @objc private func clearField() {
        field.text = ""
    }
}
