import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'owner.dart';

void main() => runApp(const MeldnetApp());
const ink = Color(0xFF08080A);
const paper = Color(0xFFF1EDE5);
const accent = Color(0xFF22BEC6);

class MeldnetApp extends StatelessWidget {
  const MeldnetApp({super.key, this.owner});
  final OwnerModel? owner;
  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Meldnet',
    debugShowCheckedModeBanner: false,
    theme: ThemeData(
      useMaterial3: true,
      scaffoldBackgroundColor: paper,
      colorScheme: ColorScheme.fromSeed(
        seedColor: ink,
        primary: ink,
        surface: paper,
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: Colors.white,
        border: OutlineInputBorder(borderRadius: BorderRadius.circular(14)),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(minimumSize: const Size(0, 52)),
      ),
    ),
    home: NetworkScreen(owner: owner),
  );
}

class NetworkScreen extends StatefulWidget {
  const NetworkScreen({super.key, this.owner});
  final OwnerModel? owner;
  @override
  State<NetworkScreen> createState() => _NetworkScreenState();
}

class _NetworkScreenState extends State<NetworkScreen>
    with WidgetsBindingObserver {
  late final OwnerModel model;
  Timer? timer;
  bool foreground = true;
  @override
  void initState() {
    super.initState();
    model = widget.owner ?? OwnerModel();
    WidgetsBinding.instance.addObserver(this);
    model.refresh();
    timer = Timer.periodic(const Duration(seconds: 8), (_) {
      if (foreground && !model.busy) model.refresh();
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    foreground = state == AppLifecycleState.resumed;
    if (foreground) model.refresh();
  }

  @override
  void dispose() {
    timer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    if (widget.owner == null) model.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: model,
    builder: (context, _) {
      final selected = model.selected;
      return Scaffold(
        appBar: AppBar(
          backgroundColor: paper,
          titleSpacing: 24,
          title: Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 10,
                  vertical: 7,
                ),
                decoration: BoxDecoration(
                  color: ink,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Image.asset(
                  'assets/brand/meld-logo.png',
                  width: 80,
                  height: 34,
                  fit: BoxFit.contain,
                  semanticLabel: 'Meld',
                  errorBuilder: (_, _, _) => const Text(
                    'meld',
                    style: TextStyle(
                      color: Colors.white,
                      fontWeight: FontWeight.w900,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 12),
              const Text('/ net', style: TextStyle(fontSize: 18)),
            ],
          ),
          actions: [
            IconButton(
              tooltip: 'Refresh status',
              onPressed: model.busy ? null : model.refresh,
              icon: const Icon(Icons.refresh),
            ),
            const SizedBox(width: 12),
          ],
        ),
        body: RefreshIndicator(
          onRefresh: model.refresh,
          child: ListView(
            padding: const EdgeInsets.fromLTRB(24, 24, 24, 32),
            children: [
              Center(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 760),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      const Text(
                        'YOUR PRIVATE NETWORK',
                        style: TextStyle(
                          fontSize: 11,
                          letterSpacing: 2.2,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      const SizedBox(height: 12),
                      const Text(
                        'Closer, wherever\nyou are.',
                        style: TextStyle(
                          fontSize: 38,
                          height: 1.1,
                          fontWeight: FontWeight.w700,
                          letterSpacing: -1.6,
                        ),
                      ),
                      const SizedBox(height: 14),
                      const Text(
                        'A secure path to your devices through your trusted primary.',
                        style: TextStyle(color: Color(0xFF62666D), height: 1.5),
                      ),
                      const SizedBox(height: 24),
                      if (model.message != null)
                        _notice(model.message!, Icons.info_outline),
                      if (model.snapshot?.hasWarning == true)
                        _notice(
                          'Meldnet needs attention. Check the network status and refresh.',
                          Icons.warning_amber,
                        ),
                      if (model.stale && model.snapshot != null)
                        _notice(
                          'Status is stale. Connection controls are paused until refresh succeeds.',
                          Icons.history,
                        ),
                      if (model.snapshot == null)
                        Container(
                          padding: const EdgeInsets.all(28),
                          decoration: _box(ink),
                          child: const Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Icon(
                                Icons.shield_outlined,
                                color: accent,
                                size: 40,
                              ),
                              SizedBox(height: 18),
                              Text(
                                'Waiting for the VPN service',
                                style: TextStyle(
                                  color: Colors.white,
                                  fontSize: 22,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                              SizedBox(height: 10),
                              Text(
                                'Observed memberships and tunnel status appear when the native service is available.',
                                style: TextStyle(
                                  color: Colors.white70,
                                  height: 1.5,
                                ),
                              ),
                            ],
                          ),
                        ),
                      if (model.snapshot != null &&
                          model.snapshot!.profiles.isEmpty)
                        Container(
                          padding: const EdgeInsets.all(28),
                          decoration: _box(ink),
                          child: const Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Icon(Icons.hub_outlined, color: accent, size: 42),
                              SizedBox(height: 22),
                              Text(
                                'Make yourself at home.',
                                style: TextStyle(
                                  color: Colors.white,
                                  fontSize: 25,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                              SizedBox(height: 10),
                              Text(
                                'Join a network with an enrollment key from its primary. Your phone gets its own private identity.',
                                style: TextStyle(
                                  color: Colors.white70,
                                  height: 1.6,
                                ),
                              ),
                            ],
                          ),
                        ),
                      if (selected != null) ...[
                        DropdownButtonFormField<String>(
                          initialValue: selected.id,
                          key: ValueKey(selected.id),
                          decoration: const InputDecoration(
                            labelText: 'Membership',
                          ),
                          items: model.snapshot!.profiles
                              .map(
                                (p) => DropdownMenuItem(
                                  value: p.id,
                                  child: Text(p.id),
                                ),
                              )
                              .toList(),
                          onChanged: (id) {
                            if (id != null) model.select(id);
                          },
                        ),
                        const SizedBox(height: 16),
                        _connection(selected),
                        const SizedBox(height: 26),
                        Row(
                          children: [
                            const Expanded(
                              child: Text(
                                'Devices',
                                style: TextStyle(
                                  fontSize: 24,
                                  fontWeight: FontWeight.w700,
                                ),
                              ),
                            ),
                            Text(
                              '${selected.peers.length}',
                              style: const TextStyle(color: Colors.black54),
                            ),
                          ],
                        ),
                        const SizedBox(height: 8),
                        const Text(
                          'A listed device is not proof of reachability.',
                          style: TextStyle(color: Colors.black54, fontSize: 12),
                        ),
                        const SizedBox(height: 14),
                        if (selected.peers.isEmpty)
                          _notice(
                            'No device evidence is available yet.',
                            Icons.devices,
                          ),
                        ...selected.peers.map(_peer),
                        const SizedBox(height: 12),
                        if (model.snapshot!.canAutoConnect)
                          SwitchListTile(
                            contentPadding: EdgeInsets.zero,
                            title: const Text('Connect at startup'),
                            subtitle: const Text(
                              'Saved owner preference; separate from OS always-on policies.',
                            ),
                            value: selected.autoConnect,
                            onChanged: model.busy || model.stale
                                ? null
                                : (enabled) => model.mutate('setAutoConnect', {
                                    'id': selected.id,
                                    'enabled': enabled,
                                  }),
                          ),
                      ],
                      const SizedBox(height: 22),
                      OutlinedButton.icon(
                        onPressed:
                            model.busy ||
                                model.stale ||
                                model.snapshot?.canJoin != true
                            ? null
                            : () => _join(context),
                        icon: const Icon(Icons.add),
                        label: const Text('Join a network'),
                      ),
                      const SizedBox(height: 24),
                      const Text(
                        'ONE ACTIVE NETWORK · OWNED BY YOUR PHONE',
                        textAlign: TextAlign.center,
                        style: TextStyle(
                          color: Colors.black45,
                          fontSize: 10,
                          letterSpacing: 1.1,
                        ),
                      ),
                      const SizedBox(height: 8),
                      const Text(
                        'Your VPN stays on when you close this app.\nClient traffic passes through the trusted primary.',
                        textAlign: TextAlign.center,
                        style: TextStyle(
                          color: Colors.black45,
                          fontSize: 11,
                          height: 1.5,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      );
    },
  );
  BoxDecoration _box(Color color) =>
      BoxDecoration(color: color, borderRadius: BorderRadius.circular(24));
  Widget _notice(String text, IconData icon) => Container(
    margin: const EdgeInsets.only(bottom: 16),
    padding: const EdgeInsets.all(16),
    decoration: _box(const Color(0xFFEAE7DE)),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 20),
        const SizedBox(width: 12),
        Expanded(child: Text(text, style: const TextStyle(height: 1.4))),
      ],
    ),
  );
  Widget _connection(Membership p) {
    final active = model.snapshot!.activeId;
    final isActive = active == p.id;
    final otherActive = active != null && !isActive;
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: _box(ink),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: accent,
                  borderRadius: BorderRadius.circular(16),
                ),
                child: Icon(
                  isActive ? Icons.shield : Icons.shield_outlined,
                  color: ink,
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(p.name, style: const TextStyle(color: Colors.white70)),
                    const SizedBox(height: 4),
                    Text(
                      model.stale ? 'Status unavailable' : p.label,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 19,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 24),
          Text(
            p.address ?? 'Address assigned after enrollment',
            style: const TextStyle(
              color: Colors.white,
              fontSize: 25,
              letterSpacing: -0.5,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Private DNS · ${p.dnsState}',
            style: const TextStyle(color: Colors.white60, fontSize: 12),
          ),
          const SizedBox(height: 20),
          if (p.state == 'up')
            const Padding(
              padding: EdgeInsets.only(bottom: 16),
              child: Text(
                'Interface active. Peer reachability is shown separately below.',
                style: TextStyle(color: Colors.white70, height: 1.4),
              ),
            ),
          if (otherActive)
            Padding(
              padding: const EdgeInsets.only(bottom: 14),
              child: Text(
                'Disconnect $active before connecting this membership.',
                style: const TextStyle(color: accent),
              ),
            ),
          FilledButton.icon(
            style: FilledButton.styleFrom(
              backgroundColor: accent,
              foregroundColor: ink,
              disabledBackgroundColor: Colors.white12,
              disabledForegroundColor: Colors.white38,
            ),
            onPressed:
                model.busy ||
                    model.stale ||
                    !model.snapshot!.canConnect ||
                    otherActive ||
                    p.pending ||
                    p.state == 'revoked'
                ? null
                : () => model.mutate(
                    isActive ? 'disconnect' : 'connect',
                    isActive ? null : {'id': p.id},
                  ),
            icon: Icon(
              isActive ? Icons.power_settings_new : Icons.arrow_forward,
            ),
            label: Text(
              model.busy
                  ? 'Updating…'
                  : isActive
                  ? 'Disconnect'
                  : 'Connect',
            ),
          ),
        ],
      ),
    );
  }

  Widget _peer(Peer p) => Container(
    margin: const EdgeInsets.only(bottom: 10),
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
    decoration: _box(Colors.white),
    child: Row(
      children: [
        const Icon(Icons.devices, color: Color(0xFF60666D)),
        const SizedBox(width: 14),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(p.name, style: const TextStyle(fontWeight: FontWeight.w600)),
              const SizedBox(height: 5),
              if (p.hostname.isNotEmpty) ...[
                Text(
                  p.hostname,
                  style: const TextStyle(fontSize: 12, color: Colors.black54),
                ),
                const SizedBox(height: 5),
              ],
              if (p.address.isNotEmpty)
                Text(
                  'IP · ${p.address}',
                  style: const TextStyle(
                    fontSize: 12,
                    color: Color(0xFF60666D),
                  ),
                ),
              const SizedBox(height: 5),
              Text(
                p.label,
                style: const TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ],
          ),
        ),
        if (p.hostname.isNotEmpty)
          IconButton(
            tooltip: 'Copy hostname',
            icon: const Icon(Icons.copy_outlined, size: 18),
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: p.hostname));
              if (mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Hostname copied')),
                );
              }
            },
          ),
      ],
    ),
  );
  Future<void> _join(BuildContext context) => showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    backgroundColor: paper,
    builder: (_) => JoinSheet(model: model),
  );
}

class JoinSheet extends StatefulWidget {
  const JoinSheet({super.key, required this.model});
  final OwnerModel model;
  @override
  State<JoinSheet> createState() => _JoinSheetState();
}

class _JoinSheetState extends State<JoinSheet> {
  final id = TextEditingController(),
      name = TextEditingController(),
      keyInput = TextEditingController();
  final form = GlobalKey<FormState>();
  @override
  void dispose() {
    id.dispose();
    name.dispose();
    keyInput.clear();
    keyInput.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.fromLTRB(
      24,
      28,
      24,
      24 + MediaQuery.viewInsetsOf(context).bottom,
    ),
    child: SingleChildScrollView(
      child: Form(
        key: form,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                const Expanded(
                  child: Text(
                    'Join your network',
                    style: TextStyle(fontSize: 26, fontWeight: FontWeight.w700),
                  ),
                ),
                IconButton(
                  tooltip: 'Close',
                  onPressed: () => Navigator.pop(context),
                  icon: const Icon(Icons.close),
                ),
              ],
            ),
            const SizedBox(height: 8),
            const Text(
              'Use the enrollment key shared by your primary. Your identity is stored securely on your phone.',
              style: TextStyle(height: 1.5, color: Colors.black54),
            ),
            const SizedBox(height: 24),
            TextFormField(
              controller: id,
              decoration: const InputDecoration(
                labelText: 'Membership ID',
                hintText: 'work',
              ),
              autocorrect: false,
              validator: (value) =>
                  RegExp(r'^[a-z]([a-z0-9-]{0,29}[a-z0-9])?$')
                      .hasMatch(value?.trim() ?? '')
                  ? null
                  : 'Use 1–31 lowercase letters, numbers or hyphens. Start with a letter and end with a letter or number.',
            ),
            const SizedBox(height: 14),
            TextFormField(
              controller: name,
              decoration: const InputDecoration(
                labelText: 'Device name',
                hintText: 'my-phone',
              ),
              validator: (value) =>
                  RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$')
                      .hasMatch(value?.trim() ?? '')
                  ? null
                  : 'Use 1–32 letters, numbers, dots, hyphens or underscores.',
            ),
            const SizedBox(height: 14),
            TextFormField(
              controller: keyInput,
              decoration: const InputDecoration(labelText: 'Enrollment key'),
              obscureText: true,
              enableSuggestions: false,
              autocorrect: false,
              keyboardType: TextInputType.visiblePassword,
              validator: (value) =>
                  value != null &&
                      value.trim().startsWith('meldnet1.') &&
                      value.trim().length <= 8192
                  ? null
                  : 'Enter a valid Meldnet enrollment key.',
            ),
            const SizedBox(height: 22),
            FilledButton(
              onPressed: () {
                if (!form.currentState!.validate()) return;
                final arguments = <String, Object?>{
                  'id': id.text.trim(),
                  'name': name.text.trim(),
                  'key': keyInput.text.trim(),
                  'connect': false,
                  'autoConnect': false,
                };
                keyInput.clear();
                Navigator.pop(context);
                widget.model.mutate('join', arguments);
              },
              child: const Text('Join securely'),
            ),
            const SizedBox(height: 12),
            const Text(
              'Enrollment does not connect automatically.',
              textAlign: TextAlign.center,
              style: TextStyle(color: Colors.black54, fontSize: 12),
            ),
          ],
        ),
      ),
    ),
  );
}
