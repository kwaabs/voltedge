pluginManagement {
    val flutterSdkPath =
        run {
            val properties = java.util.Properties()
            file("local.properties").inputStream().use { properties.load(it) }
            val flutterSdkPath = properties.getProperty("flutter.sdk")
            require(flutterSdkPath != null) { "flutter.sdk not set in local.properties" }
            flutterSdkPath
        }

    includeBuild("$flutterSdkPath/packages/flutter_tools/gradle")

    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

plugins {
    id("dev.flutter.flutter-plugin-loader") version "1.0.0"
    // Pinned to the stable AGP 8.x line (no built-in Kotlin), NOT the AGP 9.0.1 the
    // template scaffolded. AGP 9 forces android.builtInKotlin=true, which requires EVERY
    // native plugin to have migrated off the classic kotlin-android plugin -- and most of
    // the ecosystem (jni, flutter_secure_storage, ...) has not, so they fail to configure.
    // 8.13.2 is the exact AGP maplibre_gl 0.27 pins/tests against, supports its compileSdk
    // 36 / Java 21, and on 8.x maplibre_gl's `if (agpMajor < 9) apply 'kotlin-android'`
    // guard fires so it and all classic plugins build. Kotlin 2.3.x is required (not 2.1.x):
    // maplibre_gl's transitive play-services-location 21.4.0 ships Kotlin 2.3.0 metadata a
    // 2.1 compiler can't read, and Flutter wants >= 2.2.20. Kotlin 2.3.0's supported AGP
    // range is 8.2.2-8.13.0, so 2.3.20 + AGP 8.13.2 + Gradle 8.14 is a fully-supported triple.
    id("com.android.application") version "8.13.2" apply false
    id("org.jetbrains.kotlin.android") version "2.3.20" apply false
}

include(":app")
