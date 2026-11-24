import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

Future<void> copyAnnouncementInboxLink(BuildContext context,String recipient) async {
  final current=Uri.base;
  // Flutter's default web routing uses a hash. Preserve the deployment's
  // base path, but never include login tokens, query strings or credentials.
  final initial=Uri(scheme:current.scheme,host:current.host,port:current.hasPort?current.port:null,path:current.path.isEmpty?'/':current.path).toString();
  var address=initial;
  final form=GlobalKey<FormState>();
  final link=await showDialog<String>(context:context,builder:(ctx)=>AlertDialog(
    title:Text('Inbox link · $recipient'),
    content:Form(key:form,child:Column(mainAxisSize:MainAxisSize.min,children:[
      const Text('Use the server address this person can reach. They must sign in with their own account. The link contains no login token.'),
      TextFormField(initialValue:initial,decoration:const InputDecoration(labelText:'Public server address'),onChanged:(v)=>address=v,validator:(v){
        final u=Uri.tryParse(v?.trim()??'');
        if(u==null||!['http','https'].contains(u.scheme)||u.host.isEmpty||u.userInfo.isNotEmpty||u.hasQuery||u.hasFragment)return 'Enter a valid HTTP(S) server address without a query or fragment';
        return null;
      }),
    ])),
    actions:[TextButton(onPressed:()=>Navigator.pop(ctx),child:const Text('Cancel')),FilledButton.icon(onPressed:(){
      if(form.currentState?.validate()!=true)return;
      final u=Uri.parse(address.trim());
      final base=u.path.isEmpty?'/':u.path.endsWith('/')?u.path:'${u.path}/';
      Navigator.pop(ctx,u.replace(path:base,fragment:'/settings/announcement-inbox').toString());
    },icon:const Icon(Icons.copy),label:const Text('Copy inbox link'))],
  ));
  if(link==null||!context.mounted)return;
  await Clipboard.setData(ClipboardData(text:link));
  if(context.mounted)ScaffoldMessenger.of(context).showSnackBar(SnackBar(content:Text('Inbox link copied for $recipient')));
}
