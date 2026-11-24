import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/data/auth_service.dart';

class InboxManagementScreen extends ConsumerStatefulWidget {
  const InboxManagementScreen({super.key, required this.users});
  final List<UserSummary> users;
  @override
  ConsumerState<InboxManagementScreen> createState()=>_InboxManagementState();
}
class _InboxManagementState extends ConsumerState<InboxManagementScreen> {
  static const base='/api/admin/announcements-v2/inbox';
  List<Map<String,dynamic>> items=[];
  bool loading=true,busy=false,allowDelete=true;
  String? error;
  final search=TextEditingController();
  @override
  void initState(){super.initState();load();}
  @override
  void dispose(){search.dispose();super.dispose();}
  List<Map<String,dynamic>> maps(dynamic data)=>(data as List).map((e)=>Map<String,dynamic>.from(e as Map)).toList();
  Future<void> load() async {
    try {
      final api=ref.read(backendClientProvider);
      final list=maps((await api.get(base)).data);
      final policy=(await api.get('$base/policy')).data as Map;
      if(mounted)setState((){items=list;allowDelete=policy['allow_delete']==true;loading=false;error=null;});
    }catch(_){if(mounted)setState((){loading=false;error='Unable to load inbox management.';});}
  }
  Future<void> run(Future<void> Function() fn) async {
    if(busy)return;
    setState(()=>busy=true);
    try {await fn();if(mounted)await load();}
    catch(_){if(mounted)setState(()=>error='Action failed. Refresh and try again.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  Future<bool> confirm(String title,String body) async=>await showDialog<bool>(context:context,builder:(ctx)=>AlertDialog(title:Text(title),content:Text(body),actions:[TextButton(onPressed:()=>Navigator.pop(ctx,false),child:const Text('Cancel')),FilledButton(onPressed:()=>Navigator.pop(ctx,true),child:const Text('Confirm'))]))==true;
  String date(dynamic raw){
    if(raw is! num||raw<=0)return 'Never';
    return DateTime.fromMillisecondsSinceEpoch(raw.toInt()*1000,isUtc:true).toLocal().toString().split('.').first;
  }
  Future<void> edit(Map<String,dynamic> item) async {
    var title='${item['title']}',body='${item['body']}';
    var pinned=item['pinned']==true,reset=false;
    var expires=(item['expires'] as num).toInt();
    final form=GlobalKey<FormState>();
    final data=await showDialog<Map<String,dynamic>>(context:context,builder:(ctx)=>StatefulBuilder(builder:(ctx,update)=>AlertDialog(title:const Text('Edit published message'),content:SingleChildScrollView(child:Form(key:form,child:Column(mainAxisSize:MainAxisSize.min,children:[
      const Text('Changes apply to remaining inbox copies, not phone alerts already delivered.'),
      TextFormField(initialValue:title,maxLength:100,decoration:const InputDecoration(labelText:'Title'),validator:(v)=>v==null||v.trim().isEmpty?'Required':null,onChanged:(v)=>title=v),
      TextFormField(initialValue:body,maxLength:500,minLines:3,maxLines:6,decoration:const InputDecoration(labelText:'Message'),validator:(v)=>v==null||v.trim().isEmpty?'Required':null,onChanged:(v)=>body=v),
      SwitchListTile(title:const Text('Pin & protect from user deletion'),value:pinned,onChanged:(v)=>update(()=>pinned=v)),
      SwitchListTile(title:const Text('Mark unread again'),value:reset,onChanged:(v)=>update(()=>reset=v)),
      ListTile(title:Text('Expires: ${date(expires)}'),subtitle:const Text('Choose expiry date (end of day)'),onTap:()async{
        final now=DateTime.now();
        final d=await showDatePicker(context:ctx,initialDate:now,firstDate:DateUtils.dateOnly(now),lastDate:now.add(const Duration(days:365)));
        if(d!=null&&ctx.mounted)update(()=>expires=DateTime(d.year,d.month,d.day,23,59,59).millisecondsSinceEpoch~/1000);
      }),
      TextButton(onPressed:()=>update(()=>expires=0),child:const Text('Remove expiry')),
    ]))),actions:[TextButton(onPressed:()=>Navigator.pop(ctx),child:const Text('Cancel')),FilledButton(onPressed:(){if(form.currentState?.validate()==true)Navigator.pop(ctx,{'title':title.trim(),'body':body.trim(),'pinned':pinned,'expires':expires,'reset_read':reset});},child:const Text('Save changes'))])));
    if(data!=null&&mounted)await run(()async{await ref.read(backendClientProvider).put('$base/${item['id']}',data:data);});
  }
  Future<void> details(Map<String,dynamic> item) async {
    try {
      final list=maps((await ref.read(backendClientProvider).get('$base/${item['id']}')).data);
      if(!mounted)return;
      await showDialog<void>(context:context,builder:(ctx)=>AlertDialog(title:Text('${item['title']} · Read status'),content:SizedBox(width:420,child:SingleChildScrollView(child:Column(mainAxisSize:MainAxisSize.min,children:[
        const Text('Read means marked read in the web inbox, not phone delivery. Deleted copies are excluded.'),
        for(final u in list)ListTile(title:Text('${u['username']}'),subtitle:Text((u['read_at'] as num)>0?'Read: ${date(u['read_at'])}':'Unread'),trailing:IconButton(tooltip:'Remove this user’s copy',icon:const Icon(Icons.delete_outline),onPressed:()async{
          if(!await confirm('Remove user copy?','Remove this message from ${u['username']} only?'))return;
          if(!ctx.mounted||!mounted)return;
          Navigator.pop(ctx);
          await run(()async{await ref.read(backendClientProvider).delete('$base/${item['id']}/users/${u['user_id']}');});
        })),
      ]))),actions:[TextButton(onPressed:()=>Navigator.pop(ctx),child:const Text('Close'))]));
    }catch(_){if(mounted)setState(()=>error='Unable to load read status.');}
  }
  Future<void> clear() async {
    int user=0,older=0;bool pinned=false;
    final data=await showDialog<Map<String,dynamic>>(context:context,builder:(ctx)=>StatefulBuilder(builder:(ctx,update)=>AlertDialog(title:const Text('Clear inbox copies'),content:SingleChildScrollView(child:Column(mainAxisSize:MainAxisSize.min,children:[
      DropdownButtonFormField<int>(initialValue:user,decoration:const InputDecoration(labelText:'Whose inbox'),items:[const DropdownMenuItem(value:0,child:Text('Everyone')),for(final u in widget.users)DropdownMenuItem(value:u.id,child:Text(u.username))],onChanged:(v)=>update(()=>user=v??0)),
      DropdownButtonFormField<int>(initialValue:older,decoration:const InputDecoration(labelText:'Messages'),items:const[DropdownMenuItem(value:0,child:Text('All ages')),DropdownMenuItem(value:30,child:Text('Older than 30 days')),DropdownMenuItem(value:90,child:Text('Older than 90 days'))],onChanged:(v)=>update(()=>older=v??0)),
      SwitchListTile(title:const Text('Include pinned messages'),value:pinned,onChanged:(v)=>update(()=>pinned=v)),
      const Text('This cannot be undone. Delivery history and scheduled messages are not affected.'),
    ])),actions:[TextButton(onPressed:()=>Navigator.pop(ctx),child:const Text('Cancel')),FilledButton(onPressed:()=>Navigator.pop(ctx,{'user_id':user,'all':user==0,'older_days':older,'include_pinned':pinned}),child:const Text('Continue'))])));
    if(data==null||!mounted)return;
    if(!await confirm('Confirm permanent inbox cleanup','Remove matching copies from ${data['all']==true?'everyone':'the selected user'}?'))return;
    if(mounted)await run(()async{await ref.read(backendClientProvider).post('$base/clear',data:data);});
  }
  @override
  Widget build(BuildContext context){
    final visible=items.where((i)=>'${i['title']} ${i['body']}'.toLowerCase().contains(search.text.toLowerCase()));
    return Scaffold(appBar:AppBar(title:const Text('Inbox management'),actions:[IconButton(onPressed:busy?null:load,icon:const Icon(Icons.refresh),tooltip:'Refresh')]),
      body:Center(child:ConstrainedBox(constraints:const BoxConstraints(maxWidth:840),child:ListView(padding:const EdgeInsets.all(20),children:[
        Text('PUBLISHED ANNOUNCEMENTS',style:Theme.of(context).textTheme.labelLarge),
        const Text('Manage inbox copies independently of push delivery history.'),
        SwitchListTile(title:const Text('Allow users to delete their messages'),subtitle:const Text('Pinned messages are always protected.'),value:allowDelete,onChanged:busy||loading?null:(v)=>run(()async{await ref.read(backendClientProvider).put('$base/policy',data:{'allow_delete':v});})),
        OutlinedButton.icon(onPressed:busy?null:clear,icon:const Icon(Icons.delete_sweep),label:const Text('Clear user inboxes')),
        TextField(controller:search,decoration:const InputDecoration(prefixIcon:Icon(Icons.search),hintText:'Search published messages'),onChanged:(_)=>setState((){})),
        if(error!=null)Text(error!),
        if(loading)const LinearProgressIndicator(),
        if(!loading&&visible.isEmpty)const Padding(padding:EdgeInsets.all(16),child:Text('No published messages found.')),
        for(final i in visible)Card(child:Padding(padding:const EdgeInsets.all(12),child:Column(crossAxisAlignment:CrossAxisAlignment.start,children:[
          Row(children:[if(i['pinned']==true)const Padding(padding:EdgeInsets.only(right:8),child:Icon(Icons.push_pin)),Expanded(child:Text('${i['title']}',style:Theme.of(context).textTheme.titleMedium))]),
          Text('${i['body']}'),const SizedBox(height:8),
          Text('${i['read']} of ${i['count']} remaining copies read · Expires: ${date(i['expires'])}'),
          Wrap(children:[
            TextButton.icon(onPressed:busy?null:()=>edit(i),icon:const Icon(Icons.edit_outlined),label:const Text('Edit / pin / expire')),
            TextButton.icon(onPressed:busy?null:()=>details(i),icon:const Icon(Icons.people_outline),label:const Text('Read status')),
            TextButton.icon(onPressed:busy?null:()async{if(await confirm('Recall announcement?','Remove all remaining inbox copies? Delivered phone alerts cannot be recalled.')&&mounted)await run(()async{await ref.read(backendClientProvider).delete('$base/${i['id']}');});},icon:const Icon(Icons.undo),label:const Text('Recall')),
          ]),
        ]))),
      ]))),
    );
  }
}
