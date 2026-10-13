plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "si.meld.meldnet"
    compileSdk = 36
    ndkVersion = "28.2.13676358"

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "si.meld.meldnet"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        minSdk = 30
        ndk { abiFilters.clear(); abiFilters.addAll(listOf("arm64-v8a", "x86_64")) }
        targetSdk = 36
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    sourceSets.getByName("main").jniLibs.srcDir("../../native/build/jniLibs")
    sourceSets.getByName("main").assets.srcDir("../../native/licenses")
    packaging.jniLibs.keepDebugSymbols += "**/libwg-go.so"

    buildTypes {
        release {
            // Release signing must be supplied by the distributor; never reuse debug keys.
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

// Use official Java classes but deliberately exclude the unmodified AAR JNI backend.
// The only libwg-go.so packaged is the fail-closed DNS-aware build from mobile/native.
val wireguardArchive by configurations.creating { isTransitive = false }
val wireguardClasses = tasks.register<Copy>("extractWireguardClasses") {
    from({ zipTree(wireguardArchive.singleFile) }) { include("classes.jar") }
    into(layout.buildDirectory.dir("wireguard"))
}
dependencies {
    wireguardArchive("com.wireguard.android:tunnel:1.0.20260102@aar")
    implementation(files(wireguardClasses.map { it.destinationDir.resolve("classes.jar") }).builtBy(wireguardClasses))
    implementation("androidx.annotation:annotation:1.9.1")
    implementation("androidx.collection:collection:1.5.0")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20250517")
}

val verifyMobileNative = tasks.register("verifyMobileNative") {
    doLast {
        for (abi in listOf("arm64-v8a", "x86_64")) {
            check(file("../../native/build/jniLibs/$abi/libwg-go.so").isFile) {
                "Missing Meldnet DNS-aware WireGuard backend for $abi. Run scripts/build-mobile-native.py first."
            }
        }
    }
}
tasks.named("preBuild") { dependsOn(verifyMobileNative) }
