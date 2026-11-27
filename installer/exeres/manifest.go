package exeres

// Manifest is the installer program's application manifest (docs/installer.md §3.1; elements and namespaces from
// https://learn.microsoft.com/en-us/windows/win32/sbscs/application-manifests):
//
//   - requestedExecutionLevel asInvoker: the program starts with the rights of whoever starts it. The first process
//     is therefore always the user at the screen, unelevated, also when that user is a standard user who will type
//     an administrator's credentials into the prompt; it asks its question, starts the elevated copy (verb runas)
//     and afterwards enables the layout itself, so the layout lands in that user's list (inst §4.3 "over the
//     shoulder", §10 notes). requireAdministrator (and highestAvailable for an administrator) would elevate the
//     whole program at start, and after an over-the-shoulder elevation that process is the administrator, whose
//     list must not get the layout (inst §4.3). uiAccess false: the program drives no other program's windows.
//   - supportedOS Windows 10 and 11 (the only versions supported, kbl windows-install.md §0 item 4).
//   - Common Controls 6: the message boxes get the current visual style.
//   - dpiAwareness PerMonitorV2 (dpiAware true/pm before Windows 10 1607): the message boxes are drawn at the
//     monitor's scale instead of being stretched as a bitmap.
const Manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="YAKBD.Layout.Setup" version="1.0.0.0" processorArchitecture="x86"/>
  <description>YAKBD keyboard layout setup</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v2">
    <security>
      <requestedPrivileges xmlns="urn:schemas-microsoft-com:asm.v3">
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
    </asmv3:windowsSettings>
  </asmv3:application>
</assembly>
`
