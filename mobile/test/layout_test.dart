import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/services.dart';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:meldnet/main.dart';
import 'package:meldnet/owner.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    if (!const bool.fromEnvironment('CAPTURE_PREVIEW')) return;
    const fontDirectory = String.fromEnvironment('PREVIEW_FONT_DIR');
    for (final entry in {
      'Roboto': 'Roboto-Regular.ttf',
      'Ahem': 'Roboto-Regular.ttf',
      'MaterialIcons': 'MaterialIcons-Regular.otf',
    }.entries) {
      final loader = FontLoader(entry.key);
      loader.addFont(
        File('$fontDirectory/${entry.value}')
            .readAsBytes()
            .then((bytes) => ByteData.sublistView(bytes)),
      );
      await loader.load();
    }
  });
  for (final joined in [false, true]) {
    testWidgets(
      '390-wide ${joined ? 'joined' : 'empty'} layout and large text',
      (tester) async {
        tester.view.physicalSize = const Size(390, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final owner = OwnerModel(
          call: (_, _) async => {
            'version': 1,
            'activeId': joined ? 'studio' : null,
            'capabilities': {
              'join': true,
              'connect': true,
              'autoConnect': false,
            },
            'profiles': joined
                ? [
                    {
                      'id': 'studio',
                      'name': 'my-phone',
                      'state': 'up',
                      'address': '10.77.0.2',
                      'dnsState': 'ready',
                      'peers': [
                        {
                          'name': 'studio-mac',
                          'hostname': 'studio-mac.meldnet.internal',
                          'evidence': 'handshake',
                        },
                        {
                          'name': 'home-server',
                          'hostname': 'home-server.meldnet.internal',
                          'evidence': 'stale',
                        },
                      ],
                    },
                  ]
                : [],
          },
        );
        final boundaryKey = GlobalKey();
        await tester.pumpWidget(
          RepaintBoundary(
            key: boundaryKey,
            child: MeldnetApp(owner: owner),
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        if (const bool.fromEnvironment('CAPTURE_PREVIEW')) {
          await tester.runAsync(
            () => precacheImage(
              const AssetImage('assets/brand/meld-logo.png'),
              boundaryKey.currentContext!,
            ),
          );
          await tester.pumpAndSettle();
          final boundary =
              boundaryKey.currentContext!.findRenderObject()!
                  as RenderRepaintBoundary;
          boundary.markNeedsPaint();
          await tester.pump();
          await tester.runAsync(() async {
            final image = await boundary.toImage(pixelRatio: 2);
            final bytes = await image.toByteData(
              format: ui.ImageByteFormat.png,
            );
            final target = File(
              '../bin/mobile-${joined ? 'preview' : 'empty'}.png',
            );
            await target.parent.create(recursive: true);
            await target.writeAsBytes(bytes!.buffer.asUint8List());
            image.dispose();
          });
        }
        tester.platformDispatcher.textScaleFactorTestValue = 1.5;
        addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
        await tester.pumpAndSettle();
        await tester.drag(find.byType(ListView), const Offset(0, -500));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
        owner.dispose();
      },
    );
  }
}
