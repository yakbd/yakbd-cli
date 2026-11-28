import UIKit

// The check keyboard: rows of buttons from layout.json, each with the key id as its accessibility identifier, so
// a UI test can tap it at its place on screen. A long press opens a bar of choices inside the keyboard view.
class KeyboardViewController: UIInputViewController {
    private(set) var engine: KeyEngine?
    private var keysByID: [String: CheckKey] = [:]
    private var choiceTexts: [String: String] = [:]
    private var choiceBar: UIStackView?
    private var globeButtons: [UIButton] = []

    override func viewDidLoad() {
        super.viewDidLoad()
        let height = view.heightAnchor.constraint(equalToConstant: 260)
        height.priority = UILayoutPriority(999)
        height.isActive = true

        guard let layout = CheckLayout.load(from: Bundle(for: KeyboardViewController.self)) else {
            let label = UILabel()
            label.text = "layout.json missing or invalid"
            label.accessibilityIdentifier = "layout-missing"
            label.frame = CGRect(x: 8, y: 8, width: 300, height: 30)
            view.addSubview(label)
            return
        }
        engine = KeyEngine(layout: layout)

        let column = UIStackView()
        column.axis = .vertical
        column.distribution = .fillEqually
        column.spacing = 6
        column.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(column)
        NSLayoutConstraint.activate([
            column.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 4),
            column.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -4),
            // Room above the rows for the long-press choices.
            column.topAnchor.constraint(equalTo: view.topAnchor, constant: 48),
            column.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -4),
        ])
        for row in layout.rows {
            let line = UIStackView()
            line.axis = .horizontal
            line.distribution = .fillEqually
            line.spacing = 4
            for key in row {
                keysByID[key.id] = key
                line.addArrangedSubview(button(for: key))
            }
            column.addArrangedSubview(line)
        }
    }

    override func viewWillLayoutSubviews() {
        super.viewWillLayoutSubviews()
        for button in globeButtons {
            button.isHidden = !needsInputModeSwitchKey
        }
    }

    private func button(for key: CheckKey) -> UIButton {
        let button = UIButton(type: .system)
        let title = key.label ?? key.output ?? key.id
        button.setTitle(title, for: .normal)
        button.titleLabel?.font = .systemFont(ofSize: 22)
        button.backgroundColor = .secondarySystemBackground
        button.layer.cornerRadius = 5
        button.accessibilityIdentifier = key.id
        button.accessibilityLabel = title
        if key.special == "globe" {
            button.addTarget(self, action: #selector(handleInputModeList(from:with:)), for: .allTouchEvents)
            globeButtons.append(button)
            return button
        }
        button.addTarget(self, action: #selector(keyTapped(_:)), for: .touchUpInside)
        if let choices = key.longPress, !choices.isEmpty {
            let hold = UILongPressGestureRecognizer(target: self, action: #selector(keyHeld(_:)))
            hold.minimumPressDuration = 0.5
            button.addGestureRecognizer(hold)
        }
        return button
    }

    @objc private func keyTapped(_ sender: UIButton) {
        guard let id = sender.accessibilityIdentifier else { return }
        press(keyID: id)
    }

    func press(keyID: String) {
        guard let engine, let key = keysByID[keyID] else { return }
        hideChoices()
        switch key.special {
        case "backspace":
            engine.backspace(textDocumentProxy)
        case "space":
            engine.type(" ", into: textDocumentProxy)
        default:
            if let output = key.output {
                engine.type(output, into: textDocumentProxy)
            }
        }
    }

    @objc private func keyHeld(_ recognizer: UILongPressGestureRecognizer) {
        guard recognizer.state == .began, let id = recognizer.view?.accessibilityIdentifier else { return }
        showChoices(forKeyID: id)
    }

    // Choice buttons are "lp-<key id>-<n>", n counting from 1.
    func showChoices(forKeyID id: String) {
        hideChoices()
        guard let choices = keysByID[id]?.longPress, !choices.isEmpty else { return }
        let bar = UIStackView()
        bar.axis = .horizontal
        bar.distribution = .fillEqually
        bar.spacing = 4
        bar.translatesAutoresizingMaskIntoConstraints = false
        for (index, text) in choices.enumerated() {
            let choiceID = "lp-\(id)-\(index + 1)"
            let button = UIButton(type: .system)
            button.setTitle(text, for: .normal)
            button.titleLabel?.font = .systemFont(ofSize: 22)
            button.backgroundColor = .tertiarySystemBackground
            button.layer.cornerRadius = 5
            button.accessibilityIdentifier = choiceID
            button.accessibilityLabel = text
            button.addTarget(self, action: #selector(choiceTapped(_:)), for: .touchUpInside)
            choiceTexts[choiceID] = text
            bar.addArrangedSubview(button)
        }
        view.addSubview(bar)
        NSLayoutConstraint.activate([
            bar.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 4),
            bar.topAnchor.constraint(equalTo: view.topAnchor, constant: 4),
            bar.heightAnchor.constraint(equalToConstant: 40),
            bar.widthAnchor.constraint(equalToConstant: CGFloat(56 * choices.count)),
        ])
        choiceBar = bar
    }

    @objc private func choiceTapped(_ sender: UIButton) {
        guard let id = sender.accessibilityIdentifier, let text = choiceTexts[id] else { return }
        hideChoices()
        engine?.type(text, into: textDocumentProxy)
    }

    private func hideChoices() {
        choiceBar?.removeFromSuperview()
        choiceBar = nil
        choiceTexts = [:]
    }
}
