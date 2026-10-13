/* SPDX-License-Identifier: Apache-2.0
 *
 * Copyright © 2017-2021 Jason A. Donenfeld <Jason@zx2c4.com>. All Rights Reserved.
 */

#include <jni.h>
#include <stdlib.h>
#include <string.h>

struct go_string { const char *str; long n; };
extern int wgTurnOn(struct go_string ifname, int tun_fd, struct go_string settings);
extern void wgTurnOff(int handle);
extern int wgGetSocketV4(int handle);
extern int wgGetSocketV6(int handle);
extern char *wgGetConfig(int handle);
extern char *wgVersion();

JNIEXPORT jint JNICALL Java_com_wireguard_android_backend_GoBackend_wgTurnOn(JNIEnv *env, jclass c, jstring ifname, jint tun_fd, jstring settings)
{
	const char *ifname_str = (*env)->GetStringUTFChars(env, ifname, 0);
	size_t ifname_len = (*env)->GetStringUTFLength(env, ifname);
	const char *settings_str = (*env)->GetStringUTFChars(env, settings, 0);
	size_t settings_len = (*env)->GetStringUTFLength(env, settings);
	int ret = wgTurnOn((struct go_string){
		.str = ifname_str,
		.n = ifname_len
	}, tun_fd, (struct go_string){
		.str = settings_str,
		.n = settings_len
	});
	(*env)->ReleaseStringUTFChars(env, ifname, ifname_str);
	(*env)->ReleaseStringUTFChars(env, settings, settings_str);
	return ret;
}

JNIEXPORT void JNICALL Java_com_wireguard_android_backend_GoBackend_wgTurnOff(JNIEnv *env, jclass c, jint handle)
{
	wgTurnOff(handle);
}

JNIEXPORT jint JNICALL Java_com_wireguard_android_backend_GoBackend_wgGetSocketV4(JNIEnv *env, jclass c, jint handle)
{
	return wgGetSocketV4(handle);
}

JNIEXPORT jint JNICALL Java_com_wireguard_android_backend_GoBackend_wgGetSocketV6(JNIEnv *env, jclass c, jint handle)
{
	return wgGetSocketV6(handle);
}

JNIEXPORT jstring JNICALL Java_com_wireguard_android_backend_GoBackend_wgGetConfig(JNIEnv *env, jclass c, jint handle)
{
	jstring ret;
	char *config = wgGetConfig(handle);
	if (!config)
		return NULL;
	ret = (*env)->NewStringUTF(env, config);
	free(config);
	return ret;
}

JNIEXPORT jstring JNICALL Java_com_wireguard_android_backend_GoBackend_wgVersion(JNIEnv *env, jclass c)
{
	jstring ret;
	char *version = wgVersion();
	if (!version)
		return NULL;
	ret = (*env)->NewStringUTF(env, version);
	free(version);
	return ret;
}

/* Meldnet-specific policy, kept wholly within the native VPN owner. */
extern int meldnetSetDNS(struct go_string policy);
JNIEXPORT jint JNICALL Java_si_meld_meldnet_NativeDns_configure(JNIEnv *env, jobject c, jstring policy)
{
    const char *value = (*env)->GetStringUTFChars(env, policy, 0);
    if (!value) return -1;
    size_t length = (*env)->GetStringUTFLength(env, policy);
    int result = meldnetSetDNS((struct go_string){ .str = value, .n = length });
    (*env)->ReleaseStringUTFChars(env, policy, value);
    return result;
}

extern char *meldnetControl(struct go_string request);
JNIEXPORT jstring JNICALL Java_si_meld_meldnet_NativeDns_request(JNIEnv *env, jobject c, jstring request)
{
    const char *value = (*env)->GetStringUTFChars(env, request, 0);
    if (!value) return NULL;
    size_t length = (*env)->GetStringUTFLength(env, request);
    char *response = meldnetControl((struct go_string){ .str = value, .n = length });
    (*env)->ReleaseStringUTFChars(env, request, value);
    if (!response) return NULL;
    jstring result = (*env)->NewStringUTF(env, response);
    free(response);
    return result;
}
