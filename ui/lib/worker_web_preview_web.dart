import 'dart:html' as html;
import 'dart:ui_web' as ui_web;

import 'package:flutter/material.dart';

class WorkerWebPreview extends StatefulWidget {
  const WorkerWebPreview({super.key, required this.url});

  final String url;

  @override
  State<WorkerWebPreview> createState() => _WorkerWebPreviewState();
}

class _WorkerWebPreviewState extends State<WorkerWebPreview> {
  static int _nextId = 0;

  late final String _viewType = 'worker-web-preview-${_nextId++}';
  late final html.IFrameElement _iframe;

  @override
  void initState() {
    super.initState();
    _iframe = html.IFrameElement()
      ..style.border = '0'
      ..style.width = '100%'
      ..style.height = '100%'
      ..allow = 'clipboard-read; clipboard-write'
      ..src = widget.url;
    ui_web.platformViewRegistry.registerViewFactory(
      _viewType,
      (int viewId) => _iframe,
    );
  }

  @override
  void didUpdateWidget(covariant WorkerWebPreview oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.url != widget.url) {
      _iframe.src = widget.url;
    }
  }

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(6),
      child: HtmlElementView(viewType: _viewType),
    );
  }
}
