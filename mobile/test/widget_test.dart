import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:meldnet/main.dart';
import 'package:meldnet/owner.dart';

Map<String, Object?> snapshot({String? active, bool startup = false}) => {
  'version': 1,
  'activeId': active,
  'capabilities': {'join': true, 'connect': true, 'autoConnect': startup},
  'profiles': [
    for (final id in ['work', 'home'])
      {
        'id': id,
        'name': 'My phone',
        'state': id == active ? 'up' : 'disconnected',
        'address': '10.77.0.2',
        'dnsState': 'ready',
        'peers': [
          {
            'name': 'Desktop',
            'hostname': 'desktop.meldnet.internal',
            'address': '10.77.0.3',
            'evidence': 'control',
          },
        ],
      },
  ],
};

void main() {
  test('late reads cannot overwrite fresh observed state', () async {
    final first = Completer<Object?>();
    var calls = 0;
    final owner = OwnerModel(
      call: (_, _) async =>
          ++calls == 1 ? first.future : snapshot(active: 'home'),
    );
    final old = owner.refresh();
    await owner.refresh();
    first.complete(snapshot(active: 'work'));
    await old;
    expect(owner.snapshot!.activeId, 'home');
    owner.dispose();
  });
  test('failed mutations refresh state, preserve selection and suppress raw errors', () async {
    final mutation = Completer<Object?>();
    var changed = false;
    final owner = OwnerModel(
      call: (method, args) async {
        if (method != 'snapshot') return mutation.future;
        return snapshot(active: changed ? 'work' : null);
      },
    );
    await owner.refresh();
    final pending = owner.mutate('connect', {'id': 'work'});
    owner.select('home');
    changed = true;
    mutation.completeError(
      PlatformException(code: 'oops', message: 'SECRET_VALUE'),
    );
    await pending;
    expect(owner.selectedId, 'home');
    expect(owner.snapshot!.activeId, 'work');
    expect(owner.message, isNot(contains('SECRET_VALUE')));
    expect(owner.stale, false);
    owner.dispose();
  });
  test('unavailable reads retain evidence but mark it stale', () async {
    var fails = false;
    final owner = OwnerModel(
      call: (_, _) async {
        if (fails) throw const FormatException('private error');
        return snapshot(active: 'work');
      },
    );
    await owner.refresh();
    fails = true;
    await owner.refresh();
    expect(owner.stale, true);
    expect(owner.message, isNot(contains('private error')));
    owner.dispose();
  });
  testWidgets('interface evidence is honest and startup follows capability', (
    tester,
  ) async {
    final owner = OwnerModel(call: (_, _) async => snapshot(active: 'work'));
    await tester.pumpWidget(MeldnetApp(owner: owner));
    await tester.pumpAndSettle();
    expect(find.text('Tunnel interface active'), findsOneWidget);
    expect(find.text('Control-plane record'), findsOneWidget);
    expect(find.text('desktop.meldnet.internal'), findsOneWidget);
    expect(find.text('IP · 10.77.0.3'), findsOneWidget);
    expect(find.text('Connect at startup'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    owner.dispose();
  });
  testWidgets('enrollment is masked and cleared before bridge invocation', (
    tester,
  ) async {
    const secret = 'meldnet1.synthetic-key-only';
    Map<String, Object?>? submitted;
    final owner = OwnerModel(
      call: (method, args) async {
        if (method == 'join') submitted = Map<String, Object?>.from(args!);
        return snapshot();
      },
    );
    await owner.refresh();
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: JoinSheet(model: owner)),
      ),
    );
    final fields = find.byType(TextFormField);
    await tester.enterText(fields.at(0), 'work');
    await tester.enterText(fields.at(1), 'Phone');
    await tester.enterText(fields.at(2), secret);
    final editable = tester
        .widgetList<EditableText>(find.byType(EditableText))
        .last;
    expect(editable.obscureText, true);
    await tester.ensureVisible(find.text('Join securely'));
    for (final invalidId in ['work-', 'work_team', 'a' * 32]) {
      await tester.enterText(fields.at(0), invalidId);
      await tester.ensureVisible(find.text('Join securely'));
      await tester.tap(find.text('Join securely'));
      await tester.pumpAndSettle();
      expect(submitted, isNull);
      expect(find.textContaining('Use 1–31'), findsOneWidget);
    }
    await tester.enterText(fields.at(0), 'work');
    await tester.ensureVisible(find.text('Join securely'));
    await tester.tap(find.text('Join securely'));
    await tester.pumpAndSettle();
    expect(editable.controller.text, isEmpty);
    expect(submitted!['key'], secret);
    expect(submitted!['connect'], false);
    await tester.pumpWidget(const SizedBox());
    owner.dispose();
  });
}
