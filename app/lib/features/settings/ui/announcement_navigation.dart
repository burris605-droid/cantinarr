import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import 'announcements_screen.dart';
import 'announcement_inbox_screen.dart';

final announcementUnreadProvider=StreamProvider.autoDispose<int>((ref) async* {
  final auth=ref.watch(authProvider).valueOrNull;
  if(auth?.isAuthenticated!=true){yield 0;return;}
  final api=ref.watch(backendClientProvider);
  var active=true;
  ref.onDispose(()=>active=false);
  while(active){
    try{
      final r=await api.get('/api/announcements/unread');
      if(!active)return;
      yield (r.data['unread'] as num).toInt();
    }catch(_){if(!active)return;yield 0;}
    await Future<void>.delayed(const Duration(seconds:12));
  }
});

class AnnouncementNavigationScreen extends ConsumerStatefulWidget {
  const AnnouncementNavigationScreen({super.key});
  @override
  ConsumerState<AnnouncementNavigationScreen> createState()=>_AnnouncementNavigationState();
}
class _AnnouncementNavigationState extends ConsumerState<AnnouncementNavigationScreen>{
  Future<List<UserSummary>>? users;
  @override
  Widget build(BuildContext context){
    if(ref.watch(authProvider).valueOrNull?.user?.isAdmin!=true)return const AnnouncementInboxScreen();
    return FutureBuilder<List<UserSummary>>(future:users??=ref.read(authProvider.notifier).listUsers(),builder:(context,snapshot){
      if(snapshot.hasError)return Scaffold(appBar:AppBar(title:const Text('Announcements')),body:Center(child:TextButton(onPressed:()=>setState(()=>users=null),child:const Text('Retry loading users'))));
      if(!snapshot.hasData)return const Center(child:CircularProgressIndicator());
      return AnnouncementsScreen(users:snapshot.data!);
    });
  }
}
