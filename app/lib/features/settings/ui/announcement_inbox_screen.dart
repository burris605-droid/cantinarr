import 'dart:async';
import 'package:flutter/material.dart';
import 'announcement_thread_screen.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

class AnnouncementInboxScreen extends ConsumerStatefulWidget {
  const AnnouncementInboxScreen({super.key});
  @override
  ConsumerState<AnnouncementInboxScreen> createState()=>_AnnouncementInboxScreenState();
}
class _AnnouncementInboxScreenState extends ConsumerState<AnnouncementInboxScreen> {
  static const base='/api/announcements/inbox';
  List<Map<String,dynamic>> messages=[];
  bool loading=true,busy=false,refreshing=false;
  String filter='all';
  Timer? timer;
  String? error;
  @override
  void initState(){super.initState();load();timer=Timer.periodic(const Duration(seconds:12),(_){if(!busy)load();});}
  @override
  void dispose(){timer?.cancel();super.dispose();}
  Future<void> load() async {
    if(refreshing)return;refreshing=true;
    try {
      final r=await ref.read(backendClientProvider).get(base);
      final items=(r.data as List).map((e)=>Map<String,dynamic>.from(e as Map)).toList();
      if(mounted)setState((){messages=items;loading=false;error=null;});
    }catch(_){if(mounted)setState((){loading=false;error='Could not load your inbox. Pull to refresh.';});}finally{refreshing=false;}
  }
  Future<void> action(String id,{bool delete=false}) async {
    if(busy)return;
    if(delete){
      final ok=await showDialog<bool>(context:context,builder:(ctx)=>AlertDialog(title:const Text('Delete message?'),content:const Text('This removes only your inbox copy.'),actions:[TextButton(onPressed:()=>Navigator.pop(ctx,false),child:const Text('Cancel')),FilledButton(onPressed:()=>Navigator.pop(ctx,true),child:const Text('Delete'))]));
      if(ok!=true||!mounted)return;
    }
    setState(()=>busy=true);
    try {
      final api=ref.read(backendClientProvider);
      if(delete){await api.delete('$base/$id');}else{await api.put('$base/$id');}
      await load();
    }catch(_){if(mounted)setState(()=>error='Inbox update failed. Refresh and try again.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  Future<void> acknowledge(String id)async{
    if(busy)return;
    final ok=await showDialog<bool>(context:context,builder:(ctx)=>AlertDialog(title:const Text('Acknowledge announcement?'),content:const Text('Confirm that you have read this announcement. The administrator can see your acknowledgment.'),actions:[TextButton(onPressed:()=>Navigator.pop(ctx,false),child:const Text('Cancel')),FilledButton(onPressed:()=>Navigator.pop(ctx,true),child:const Text('Acknowledge'))]));
    if(ok!=true||!mounted)return;
    setState(()=>busy=true);
    try{await ref.read(backendClientProvider).post('$base/$id/ack');await load();}
    catch(_){if(mounted)setState(()=>error='Acknowledgment failed.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  @override
  Widget build(BuildContext context){
    final unread=messages.where((m)=>m['read']!=true).length;
    return Scaffold(appBar:AppBar(title:Text('Announcement inbox ($unread unread)'),actions:[
      IconButton(onPressed:busy?null:()=>action('all'),icon:const Icon(Icons.done_all),tooltip:'Mark all read'),
      IconButton(onPressed:busy?null:load,icon:const Icon(Icons.refresh),tooltip:'Refresh'),
    ]),body:Center(child:ConstrainedBox(constraints:const BoxConstraints(maxWidth:760),child:RefreshIndicator(onRefresh:load,child:ListView(physics:const AlwaysScrollableScrollPhysics(),padding:const EdgeInsets.all(16),children:[
      if(error!=null)Text(error!),
      if(loading)const LinearProgressIndicator(),
      if(!loading&&messages.isEmpty&&error==null)const Padding(padding:EdgeInsets.all(32),child:Center(child:Text('No announcements yet.'))),
      Wrap(spacing:8,children:[for(final f in ['all','unread','important','urgent','maintenance','general'])ChoiceChip(label:Text(f),selected:filter==f,onSelected:(_)=>setState(()=>filter=f))]),
      for(final m in messages.where((m)=>filter=='all'||filter=='unread'&&m['read']!=true||filter==m['priority']))Card(child:Padding(padding:const EdgeInsets.all(12),child:Column(crossAxisAlignment:CrossAxisAlignment.start,children:[
        Row(children:[Icon(m['read']==true?Icons.mark_email_read_outlined:Icons.mark_email_unread_outlined),const SizedBox(width:8),Expanded(child:Text('${m['title']}',style:Theme.of(context).textTheme.titleMedium)),if(m['pinned']==true)const Icon(Icons.push_pin,size:18),if(m['read']!=true)const Chip(label:Text('New'))]),
        Text('Priority: ${m['priority']??'general'}'),
        if(m['require_ack']==true)Text(m['acknowledged']==true?'Acknowledged':'Acknowledgment required'),
        const SizedBox(height:8),SelectableText('${m['body']}'),const SizedBox(height:8),
        Text(DateTime.fromMillisecondsSinceEpoch((m['created'] as num).toInt()*1000,isUtc:true).toLocal().toString().split('.').first),
        Wrap(children:[
          if(m['read']!=true)TextButton(onPressed:busy?null:()=>action('${m['id']}'),child:const Text('Mark read')),
          if(m['require_ack']==true&&m['acknowledged']!=true)FilledButton(onPressed:busy?null:()=>acknowledge('${m['id']}'),child:const Text('Acknowledge')),
          if(m['replies_enabled']==true||(m['reply_count'] as num? ?? 0)>0)TextButton.icon(onPressed:busy?null:()async{
            await Navigator.of(context).push(MaterialPageRoute<void>(builder:(_)=>AnnouncementThreadScreen(endpoint:'$base/${m['id']}/replies',heading:'${m['title']}',admin:false,canReply:m['replies_enabled']==true)));
            if(mounted)await load();
          },icon:const Icon(Icons.forum_outlined),label:Text('Private replies (${m['reply_count']??0})')),
          if(m['can_delete']==true)TextButton(onPressed:busy?null:()=>action('${m['id']}',delete:true),child:const Text('Delete')),
        ]),
      ]))),
    ])))));
  }
}
