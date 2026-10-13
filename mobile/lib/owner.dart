import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

typedef OwnerCall = Future<Object?> Function(
  String method,
  Map<String, Object?>? arguments,
);

class Peer {
  Peer(Map<Object?, Object?> value)
    : name = value['name'] as String? ?? 'Device',
      hostname = value['hostname'] as String? ?? '',
      address = value['address'] as String? ?? '',
      evidence = value['evidence'] as String? ?? 'unknown';
  final String name, hostname, address, evidence;
  String get label => switch (evidence) {
    'self' => 'This device',
    'handshake' => 'Recent handshake',
    'control' => 'Control-plane record',
    'stale' => 'Stale evidence',
    'disconnected' => 'Disconnected',
    _ => 'Reachability unknown',
  };
}

class Membership {
  Membership(Map<Object?, Object?> value)
    : id = value['id'] as String,
      name = value['name'] as String? ?? 'My device',
      state = value['state'] as String? ?? 'disconnected',
      address = value['address'] as String?,
      dnsState = value['dnsState'] as String? ?? 'unavailable',
      autoConnect = value['autoConnect'] == true,
      pending = value['pending'] == true,
      peers = (value['peers'] as List? ?? [])
          .map((p) => Peer(p as Map))
          .toList();
  final String id, name, state, dnsState;
  final String? address;
  final bool autoConnect, pending;
  final List<Peer> peers;
  String get label => switch (state) {
    'up' => 'Tunnel interface active',
    'connecting' => 'Connecting',
    'revoked' => 'Access revoked',
    'error' => 'Needs attention',
    _ => 'Disconnected',
  };
}

class OwnerSnapshot {
  OwnerSnapshot(Map<Object?, Object?> value)
    : profiles = (value['profiles'] as List? ?? [])
          .map((p) => Membership(p as Map))
          .toList(),
      activeId = value['activeId'] as String?,
      canJoin = (value['capabilities'] as Map?)?['join'] == true,
      canConnect = (value['capabilities'] as Map?)?['connect'] == true,
      canAutoConnect = (value['capabilities'] as Map?)?['autoConnect'] == true,
      hasWarning = value['warning'] != null {
    if (value['version'] != 1) {
      throw const FormatException('Unsupported owner version');
    }
  }
  final List<Membership> profiles;
  final String? activeId;
  final bool canJoin, canConnect, canAutoConnect, hasWarning;
}

/// Owns public presentation state only. The native VPN owner retains secrets.
class OwnerModel extends ChangeNotifier {
  OwnerModel({OwnerCall? call}) : _call = call ?? _nativeCall;
  static const _channel = MethodChannel('si.meld.meldnet/v1');
  static Future<Object?> _nativeCall(
    String method,
    Map<String, Object?>? args,
  ) => _channel.invokeMethod<Object?>(method, args);
  final OwnerCall _call;
  OwnerSnapshot? snapshot;
  String? selectedId, message;
  bool busy = false, stale = true, _disposed = false;
  int _request = 0;
  Membership? get selected {
    for (final p in snapshot?.profiles ?? <Membership>[]) {
      if (p.id == selectedId) return p;
    }
    return null;
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  void select(String id) {
    selectedId = id;
    _notify();
  }

  void _accept(Object? value) {
    final next = OwnerSnapshot(value as Map);
    snapshot = next;
    if (!next.profiles.any((p) => p.id == selectedId)) {
      selectedId =
          next.activeId ??
          (next.profiles.isEmpty ? null : next.profiles.first.id);
    }
    stale = false;
  }

  Future<void> refresh() async {
    final generation = ++_request;
    try {
      final value = await _call('snapshot', null);
      if (_disposed || generation != _request) return;
      _accept(value);
      message = null;
    } catch (_) {
      if (_disposed || generation != _request) return;
      stale = true;
      message = 'VPN service unavailable. Refresh to load observed status.';
    }
    _notify();
  }

  Future<void> mutate(String method, [Map<String, Object?>? args]) async {
    if (busy) return;
    busy = true;
    ++_request; // Invalidate reads begun before the mutation.
    message = null;
    _notify();
    String? failure;
    try {
      await _call(method, args);
    } catch (_) {
      // Never echo platform messages or input: errors can contain credentials.
      failure = 'The request could not be completed. Check the refreshed status before retrying.';
    } finally {
      if (method == 'join') args?.remove('key');
    }
    // A mutation may have succeeded even when its response was lost. Always
    // reload the owner, also preserving a selection changed during the request.
    await refresh();
    if (failure != null) message = failure;
    busy = false;
    _notify();
  }

  @override
  void dispose() {
    _disposed = true;
    ++_request;
    super.dispose();
  }
}
