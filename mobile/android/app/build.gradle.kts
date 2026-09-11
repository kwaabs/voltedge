plugins {
    id("com.android.application")
    // On the AGP 8.x line (built-in Kotlin off) the app's Kotlin MainActivity needs the
    // classic Kotlin Gradle plugin applied explicitly. Version comes from settings.gradle.kts.
    id("org.jetbrains.kotlin.android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "com.voltedge.voltedge_field"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.voltedge.voltedge_field"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName

        // Only build ARM ABIs. The heavy cost of this project is compiling native libs
        // (sqlite3_flutter_libs builds libsqlite3 via the NDK) for every ABI; x86/x86_64
        // are emulator-only and wasted on a physical Samsung tablet. Dropping them roughly
        // halves the native build. Narrow to just "arm64-v8a" once `adb shell getprop
        // ro.product.cpu.abi` confirms the device is 64-bit (Samsung SM-T395).
        ndk {
            abiFilters += listOf("arm64-v8a", "armeabi-v7a")
        }
    }

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}
