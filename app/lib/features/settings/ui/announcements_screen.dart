import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/data/auth_service.dart';

/// Admin-only composer. The server enforces authorization independently.
class AnnouncementsScreen extends ConsumerStatefulWidget {
  const AnnouncementsScreen({super.key, required this.users});
  final List<UserSummary> users;

  @override
  ConsumerState<AnnouncementsScreen> createState() => _AnnouncementsScreenState();
}

class _AnnouncementsScreenState extends ConsumerState<AnnouncementsScreen> {
  final _title = TextEditingController();
  final _body = TextEditingController();
  final _selected = <int>{};
  String _audience = 'self';
  bool _busy = false;
  bool _loading = true;
  String? _status;
  String? _historyError;
  List<dynamic> _history = [];

  @override
  void initState() {
    super.initState();
    _loadHistory();
  }

  @override
  void dispose() {
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  Future<void> _loadHistory() async {
    try {
      final response = await ref.read(backendClientProvider).get('/api/admin/announcements');
      if (!mounted) return;
      setState(() {
        _history = List<dynamic>.from(response.data as List);
        _loading = false;
        _historyError = null;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _historyError = 'History unavailable. Check the server version and administrator permissions.';
      });
    }
  }

  Future<void> _send() async {
    if (_busy || _title.text.trim().isEmpty || _body.text.trim().isEmpty ||
        (_audience == 'selected' && _selected.isEmpty)) return;
    final audience = _audience;
    final title = _title.text.trim();
    final body = _body.text.trim();
    final ids = _selected.toList();
    final target = audience == 'all' ? 'all eligible users' :
        audience == 'self' ? 'your registered devices' : '${ids.length} selected users';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Confirm announcement'),
        content: Text('Send "$title" to $target?'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Send')),
        ],
      ),
    );
    if (!mounted || confirmed != true || _busy) return;
    setState(() { _busy = true; _status = null; });
    try {
      final response = await ref.read(backendClientProvider).post(
        '/api/admin/announcements',
        data: {'audience': audience, 'title': title, 'body': body,
          'user_ids': audience == 'selected' ? ids : <int>[]},
      );
      final result = Map<String, dynamic>.from(response.data as Map);
      if (!mounted) return;
      setState(() {
        _status = '${result['sent']} devices accepted by gateway, '
            '${result['failed']} failed, ${result['skipped']} users skipped. '
            'Gateway acceptance does not guarantee the phone displayed the alert.';
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _status = 'Send was not confirmed. Check history before retrying to avoid duplicate notifications.');
    } finally {
      if (mounted) {
        setState(() => _busy = false);
        await _loadHistory();
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final canSend = !_busy && _title.text.trim().isNotEmpty &&
        _body.text.trim().isNotEmpty &&
        (_audience != 'selected' || _selected.isNotEmpty);
    return Scaffold(
      appBar: AppBar(title: const Text('Server announcements')),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 760),
          child: ListView(
            padding: const EdgeInsets.all(20),
            children: [
              Text('Burris Edition', style: Theme.of(context).textTheme.labelLarge),
              const SizedBox(height: 8),
              const Text('Send a push notification to Cantinarr users. Only active devices with push enabled are eligible. Start with a test to yourself.'),
              const SizedBox(height: 24),
              DropdownButtonFormField<String>(
                initialValue: _audience,
                decoration: const InputDecoration(labelText: 'Recipients'),
                items: const [
                  DropdownMenuItem(value: 'self', child: Text('Test to myself')),
                  DropdownMenuItem(value: 'all', child: Text('Everyone')),
                  DropdownMenuItem(value: 'selected', child: Text('Selected users')),
                ],
                onChanged: _busy ? null : (value) => setState(() => _audience = value ?? 'self'),
              ),
              if (_audience == 'selected')
                for (final user in widget.users)
                  CheckboxListTile(
                    title: Text(user.username),
                    value: _selected.contains(user.id),
                    onChanged: _busy ? null : (checked) => setState(() {
                      if (checked == true) { _selected.add(user.id); }
                      else { _selected.remove(user.id); }
                    }),
                  ),
              const SizedBox(height: 12),
              TextField(controller: _title, enabled: !_busy, maxLength: 100,
                decoration: const InputDecoration(labelText: 'Notification title'),
                onChanged: (_) => setState(() {})),
              TextField(controller: _body, enabled: !_busy, maxLength: 500,
                minLines: 3, maxLines: 6,
                decoration: const InputDecoration(labelText: 'Message'),
                onChanged: (_) => setState(() {})),
              const SizedBox(height: 16),
              Text('Notification preview', style: Theme.of(context).textTheme.titleMedium),
              Card(child: ListTile(
                leading: const Icon(Icons.notifications_active_outlined),
                title: Text(_title.text.isEmpty ? 'Your title' : _title.text),
                subtitle: Text(_body.text.isEmpty ? 'Your message' : _body.text),
              )),
              const SizedBox(height: 12),
              FilledButton.icon(
                onPressed: canSend ? _send : null,
                icon: Icon(_busy ? Icons.hourglass_top : Icons.send_outlined),
                label: Text(_busy ? 'Sending…' : _audience == 'self' ? 'Send test' : 'Send announcement'),
              ),
              if (_status != null) Padding(
                padding: const EdgeInsets.symmetric(vertical: 12),
                child: Text(_status!),
              ),
              const Divider(height: 40),
              Row(children: [
                Expanded(child: Text('Announcement history', style: Theme.of(context).textTheme.titleLarge)),
                IconButton(tooltip: 'Refresh history', onPressed: _busy ? null : _loadHistory,
                  icon: const Icon(Icons.refresh)),
              ]),
              if (_loading) const LinearProgressIndicator()
              else if (_historyError != null) Text(_historyError!)
              else if (_history.isEmpty) const Text('No announcements yet.')
              else for (final item in _history)
                Card(child: ListTile(
                  title: Text('${item['title']}'),
                  subtitle: Text('${item['body']}\n${item['created']} UTC · ${item['audience']} · ${item['status']}\nGateway accepted: ${item['sent']} · Failed: ${item['failed']} · Skipped users: ${item['skipped']}'),
                  isThreeLine: true,
                )),
            ],
          ),
        ),
      ),
    );
  }
}
