# The apk package: Android packages without the Android SDK

Package `apk` writes and rewrites Android application packages (APKs) with the Go standard library only, so that
`yakbd` builds installable Android keyboards on any machine without the Android SDK. It holds no keyboard knowledge;
the targets `android` (a code-free APK with key character maps) and `androidime` (a prebuilt runtime APK repackaged
for one layout) use it. The formats come from AOSP's public documentation and sources (Apache-2.0), read for facts
only (`CLEAN-ROOM.md`, "Android").

## 1. What it does

| part | API | what it writes or reads |
|---|---|---|
| zip | `Archive`, `Entry`, `Read`, `Archive.Put`, `Archive.Remove` | entries in order, deflated or stored; a stored entry's data starts on a 4-byte boundary (16 KiB for `.so` files), padded with the zipalign extra field `0xd935`; fixed time stamps; no zip64. `resources.arsc` must be stored |
| binary XML | `ParseXML`, `EncodeXML`, `Node`, `Attr`, `Value`, `E`, `A`, `AndroidAttr` | `AndroidManifest.xml` and `res/xml` files. The reader takes UTF-8 and UTF-16 string pools and refuses what it would not write back with the same meaning (unknown chunks, unbalanced elements or namespaces). The writer: one UTF-16 pool, the attribute names with resource ids first (in resource-map order, ascending id; an attribute without an id never shares their slots), attributes sorted by resource id |
| resource table | `ResourceTable`, `TablePackage` | a minimal `resources.arsc`: one package (id 0x7f), file resources such as `xml` and `raw` in the default configuration, ids `0x7fTTEEEE` (types from 1 in order of first use, entries sorted by name) |
| rewrite | `RenamePackage`, `SetLabel`, `Repackage` | a new package name: `manifest@package`, permission names and the permissions, task affinities and process names that start with the old name (AndroidX's `<pkg>.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION`, so two repackaged APKs install side by side), provider authorities; class names never change (relative ones are written in full with the old package first). The application label as literal text. Files added or replaced (assets). `resources.arsc` keeps its package name |
| signing | `Archive.Sign`, `Signer`, `LoadSigner`, `ParseSigner`, `GenerateSigner`, `DerivedCertificate` | JAR signing (v1, SHA-256, Android 4.3+), APK Signature Scheme v2 (Android 7+) with the stripping-protection attribute, v3 (Android 9+, no rotation); earlier signatures dropped. RSA (PKCS #1 v1.5, deterministic) or ECDSA P-256 |
| report | `ReportSigned`, `KeyFlag`, `SignerFor` | the `apk.signed` and `apk.testKey` entries and the `-key` flag ([targets.md](targets.md) §5, [report-file.md](report-file.md) §7) |

## 2. Signing identity

Android compares an APK's signing **certificate** on every update, and a builder registers the certificate's SHA-256
fingerprint for developer verification. So the same key must always come with the same certificate:

- a PEM file with a key and its certificate (for example exported from the builder's keystore) uses that certificate;
- an RSA key alone gets `DerivedCertificate`: subject `CN=Keyboard layout`, serial number from the key's hash,
  validity 2026-01-01 to 2126-01-01. Its signature is deterministic, so the fingerprint is the same on every build;
- an EC key alone is refused: ECDSA signatures differ on every run, so a derived certificate would too;
- without `-key`, `GenerateSigner` makes an RSA 2048 test key kept nowhere: the APK installs, but no later build can
  update it (warning `apk.testKey`).

A builder who keeps an RSA key without its certificate depends on this tool writing the derived certificate byte for
byte the same in every later version. Safer: keep the certificate in the key file once (`Signer.PEM()` writes key
and certificate together; a keystore export works too), so the identity no longer depends on the tool.

A private key is never committed, not even a test key: the tests and the CI control generate theirs per run.

## 3. Limits

No zip64 (entries and archives below 4 GiB, at most 65,535 entries), no resource configurations other than the
default, no styled strings, no key rotation (v3.1, proof-of-rotation), no v4 signatures. A runtime APK entry may be
at most 256 MiB.

## 4. The CI control

`go test ./apk` checks the structures with the package's own reader and a verifier written in the test (v1 digests,
the APK Signing Block, v2 and v3 digests and signatures), byte-stable binary XML and resource tables
(`apk/testdata/*.axml`, `*.arsc`: `go test ./apk -update` after a deliberate change), stable fingerprints, and the
rename rules. `FuzzParseXML` is the reader's fuzz target ([fuzzing.md](fuzzing.md)).

The job `apk` of `.github/workflows/ci.yml` runs `tools/apkcheck/control.sh` against the Android SDK's own tools on
the runner (the SDK is used there only, never by the tool):

1. `go run ./tools/apkcheck -o DIR` writes a code-free test APK (a receiver of `QUERY_KEYBOARD_LAYOUTS` with one
   `.kcm`), signed once with a generated RSA test key and once with an EC key. `apksigner verify --verbose
   --min-sdk-version 21` must report v1, v2 and v3; `zipalign -c -p 4` must pass; `aapt2 dump badging`, `dump
   xmltree` (manifest, keyboard-layouts XML) and `dump resources` must show the package, the meta-data, the
   resource reference and the raw resource.
2. `aapt2 link` builds an APK from `tools/apkcheck/aapt2/` (aapt2's own string pools and attribute order);
   `tools/apkcheck -rewrite` renames its package, sets its label and adds an asset; the result must verify, pass
   zipalign, and `aapt2` must show the new package and label, the renamed permission and authority, the unchanged
   class name and the untouched `res/xml` file.

Locally the same checks ran with Debian's `apksigner` 0.9, `aapt2` and `zipalign` in a container (2026-11-28; no
`android.jar` there, so part 2 ran only in CI), and the rewrite on two IME APKs from F-Droid (not in the
repository): the manifests differed only in the package, label, the `DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION` and
the `androidx-startup` authority. Every binary XML file of those two APKs (1,390: the manifests in UTF-16, the
`res/` files in UTF-8 pools, 121 roots with several namespaces) read, wrote and read again to the same bytes.
The CI job first ran green on build-tools 37.0.0 (run 46134943831). Installing in the emulator is the `android` target's check.
