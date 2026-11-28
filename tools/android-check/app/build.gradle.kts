// The check app of tools/android-check (README.md): a text field that writes what it receives to a file,
// a minimal on-screen keyboard (input method) and an instrumentation that calls hidden keyboard-layout
// methods with the shell's permissions. No libraries.
plugins {
    id("com.android.application") version "8.7.3"
    id("org.jetbrains.kotlin.android") version "2.1.0"
}

android {
    namespace = "org.yakbd.androidcheck"
    compileSdk = 35
    defaultConfig {
        applicationId = "org.yakbd.androidcheck"
        minSdk = 34
        targetSdk = 35
        versionCode = 1
        versionName = "1"
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}
